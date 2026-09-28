// Package window 实现带水位线的累积窗口计数器。
//
// 大窗口按固定长度对齐划分、左闭右开；每个大窗口内部按固定步长形成逐级
// 扩大的子窗口，子窗口在到期（终点不超过水位线）时输出从大窗口起点到该
// 子窗口终点的累计计数。水位线随已接收事件的最大事件时间单调前进，迟到
// 事件在其最小子窗口终点不超过水位线时被丢弃。
package window

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
)

// Config 是累积窗口计数器的参数。
type Config struct {
	// WindowSize 是大窗口固定长度，必须为正数。
	WindowSize int64
	// Step 是大窗口内子窗口的步长，必须为正数，且整除 WindowSize。
	Step int64
	// MaxWindows 是计数器中允许同时保留的（键 × 大窗口）状态数上限，必须为正数。
	MaxWindows int
}

// Output 是一个到期子窗口的一次累计输出。
// 累计区间为 [WindowStart, End)，即大窗口起点到子窗口终点（不含终点）。
type Output struct {
	Key         string
	WindowStart int64
	End         int64
	Cumulative  int64
}

// AddResult 描述一次 Add 调用的判定与本次新触发的输出。
type AddResult struct {
	Key             string
	Timestamp       int64
	WatermarkBefore int64
	WatermarkAfter  int64
	// MinEnd 是该事件被计入的最小子窗口终点（严格晚于事件时间的第一个终点）。
	MinEnd int64
	// Dropped 为 true 表示事件因迟到被丢弃（不构成拒绝）。
	Dropped bool
	// Fired 是本次调用新触发的全部子窗口输出，已按 (终点, 键) 排序。
	Fired []Output
}

// Snapshot 是计数器状态的一致性只读快照。
type Snapshot struct {
	Watermark int64
	Dropped   int64
	Outputs   []Output
}

// 可区分的拒绝原因，调用方使用 errors.Is 判定。
var (
	// ErrInvalidParameter 表示构造参数非法。
	ErrInvalidParameter = errors.New("window: invalid parameter")
	// ErrEmptyKey 表示事件键为空字符串。
	ErrEmptyKey = errors.New("window: empty key")
	// ErrTooManyWindows 表示同时保留的大窗口状态数超过 MaxWindows。
	ErrTooManyWindows = errors.New("window: too many retained windows")
)

// Accumulator 是带水位线的累积窗口计数器，零值不可用，须通过 New 构造。
type Accumulator struct {
	mu     sync.RWMutex
	cfg    Config
	logger *slog.Logger

	// 以下字段均在 mu 保护下访问。
	watermark int64
	dropped   int64
	total     int
	keys      map[string]map[int64]*windowState
	history   []Output
}

// windowState 是某个键的某个大窗口的计数状态。
// cum[j] 是区间 [start, start+j*Step) 内的累计计数，j 取 1..steps。
type windowState struct {
	start int64
	cum   []int64
	fired int
}

// Option 配置 Accumulator。
type Option func(*Accumulator)

// WithLogger 设置判定日志的输出位置。
func WithLogger(logger *slog.Logger) Option {
	return func(a *Accumulator) { a.logger = logger }
}

// New 校验参数并构造计数器。
func New(cfg Config, opts ...Option) (*Accumulator, error) {
	if cfg.WindowSize <= 0 {
		return nil, fmt.Errorf("%w: window size must be positive, got %d", ErrInvalidParameter, cfg.WindowSize)
	}
	if cfg.Step <= 0 {
		return nil, fmt.Errorf("%w: step must be positive, got %d", ErrInvalidParameter, cfg.Step)
	}
	if cfg.WindowSize%cfg.Step != 0 {
		return nil, fmt.Errorf("%w: step %d must divide window size %d", ErrInvalidParameter, cfg.Step, cfg.WindowSize)
	}
	if cfg.MaxWindows <= 0 {
		return nil, fmt.Errorf("%w: max windows must be positive, got %d", ErrInvalidParameter, cfg.MaxWindows)
	}
	a := &Accumulator{
		cfg:       cfg,
		logger:    slog.Default(),
		watermark: math.MinInt64, // 初始水位线为负无穷：任何首个事件都不会被判迟到
		keys:      make(map[string]map[int64]*windowState),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	return a, nil
}

// floorDiv 返回 x/y 向下取整的商（y 必须为正），负时间戳同样对齐到窗口起点。
func floorDiv(x, y int64) int64 {
	q := x / y
	if x%y != 0 && x < 0 {
		q--
	}
	return q
}

// Add 录入一个事件并推进水位线、触发到期子窗口。
//
// 事件按时间戳归入唯一大窗口 [start, start+WindowSize)，并计入该窗口内
// 所有终点严格晚于时间戳的子窗口。若其最小子窗口终点不超过当前水位线，
// 事件作为迟到数据被丢弃（Dropped=true，并非错误）。被拒绝的输入（空键、
// 超出窗口保留上限）返回错误且不改变任何状态。
func (a *Accumulator) Add(key string, timestamp int64) (AddResult, error) {
	if key == "" {
		err := fmt.Errorf("%w: key must not be empty (timestamp=%d)", ErrEmptyKey, timestamp)
		a.logger.Info("window.Add rejected", "key", key, "timestamp", timestamp, "reason", "empty-key")
		return AddResult{Key: key, Timestamp: timestamp}, err
	}

	// 归属大窗口与最小子窗口终点（锁外纯计算）。
	windowStart := floorDiv(timestamp, a.cfg.WindowSize) * a.cfg.WindowSize
	offset := timestamp - windowStart // 0 <= offset < WindowSize，负时间戳同样成立
	firstStep := offset/a.cfg.Step + 1
	minEnd := windowStart + firstStep*a.cfg.Step

	a.mu.Lock()
	defer a.mu.Unlock()

	res := AddResult{
		Key:             key,
		Timestamp:       timestamp,
		WatermarkBefore: a.watermark,
		MinEnd:          minEnd,
	}

	// 迟到丢弃：最小子窗口终点已不超过水位线，事件不可能再影响任何输出。
	if minEnd <= a.watermark {
		a.dropped++
		res.WatermarkAfter = a.watermark
		res.Dropped = true
		a.logger.Info("window.Add dropped",
			"key", key, "timestamp", timestamp,
			"windowStart", windowStart, "minEnd", minEnd,
			"watermark", a.watermark, "reason", "late: minEnd <= watermark")
		return res, nil
	}

	// 新水位线：随最大事件时间单调前进。
	newWatermark := a.watermark
	if timestamp > newWatermark {
		newWatermark = timestamp
	}

	// 容量裁决必须在任何状态提交之前完成：被拒绝的输入不得改变
	// 水位线、丢弃数或已输出结果。当前事件的最小子窗口终点严格
	// 晚于其时间戳且晚于旧水位线（否则已在上面丢弃），故它所属的
	// 窗口不可能在新水位线下被完整回收，只需统计既有的可回收窗口。
	windows := a.keys[key]
	ws := windows[windowStart]
	if ws == nil {
		reclaimable := 0
		for _, wm := range a.keys {
			for s, w := range wm {
				lastEnd := s + int64(len(w.cum)-1)*a.cfg.Step
				if lastEnd <= newWatermark {
					reclaimable++
				}
			}
		}
		if a.total-reclaimable+1 > a.cfg.MaxWindows {
			err := fmt.Errorf("%w: key %q window starting %d would exceed limit %d (retained=%d, reclaimable=%d)",
				ErrTooManyWindows, key, windowStart, a.cfg.MaxWindows, a.total, reclaimable)
			res.WatermarkAfter = a.watermark // 拒绝不改变水位线
			a.logger.Info("window.Add rejected",
				"key", key, "timestamp", timestamp,
				"windowStart", windowStart, "reason", "too-many-windows",
				"retained", a.total, "reclaimable", reclaimable, "limit", a.cfg.MaxWindows)
			return res, err
		}
	}

	// 裁决通过，提交：推进水位线、触发到期子窗口并回收完整窗口。
	a.watermark = newWatermark
	res.WatermarkAfter = newWatermark
	fired := a.fireDue()

	if ws == nil {
		// fireDue 可能已回收该键的全部旧窗口并摘除其 map，这里重新获取。
		windows = a.keys[key]
		if windows == nil {
			windows = make(map[int64]*windowState)
			a.keys[key] = windows
		}
		ws = &windowState{
			start: windowStart,
			cum:   make([]int64, a.cfg.WindowSize/a.cfg.Step+1),
		}
		windows[windowStart] = ws
		a.total++
	}

	// 计入所有终点严格晚于事件时间的子窗口（firstStep..steps）。
	for j := int(firstStep); j < len(ws.cum); j++ {
		ws.cum[j]++
	}

	a.history = append(a.history, fired...)
	res.Fired = fired
	a.logger.Info("window.Add accepted",
		"key", key, "timestamp", timestamp,
		"windowStart", windowStart, "minEnd", minEnd,
		"watermarkBefore", res.WatermarkBefore, "watermarkAfter", a.watermark,
		"firedCount", len(fired), "fired", fired)
	return res, nil
}

// fireDue 输出所有到期子窗口并回收已完成的大窗口，调用方须持有 a.mu。
func (a *Accumulator) fireDue() []Output {
	var fired []Output
	for key, windows := range a.keys {
		for start, ws := range windows {
			steps := int64(len(ws.cum) - 1)
			for ws.fired < len(ws.cum)-1 {
				next := ws.fired + 1
				end := start + int64(next)*a.cfg.Step
				if end > a.watermark {
					break
				}
				fired = append(fired, Output{
					Key:         key,
					WindowStart: start,
					End:         end,
					Cumulative:  ws.cum[next],
				})
				ws.fired = next
			}
			if int64(ws.fired) == steps {
				delete(windows, start)
				a.total--
			}
		}
		if len(windows) == 0 {
			delete(a.keys, key)
		}
	}
	sort.Slice(fired, func(i, j int) bool {
		if fired[i].End != fired[j].End {
			return fired[i].End < fired[j].End
		}
		return fired[i].Key < fired[j].Key
	})
	return fired
}

// Snapshot 返回逐字段一致的状态快照（水位线、丢弃数、全部已输出结果）。
func (a *Accumulator) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := Snapshot{
		Watermark: a.watermark,
		Dropped:   a.dropped,
		Outputs:   make([]Output, len(a.history)),
	}
	copy(out.Outputs, a.history)
	return out
}
