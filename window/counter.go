// Package window 提供带水位线（watermark）与迟到容限（allowed lateness）的
// 按键（key）翻滚窗口（tumbling window）计数器。
package window

import (
	"errors"
	"log/slog"
	"math"
	"sort"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidWindowSize 窗口大小非正。
	ErrInvalidWindowSize = errors.New("window: window size must be positive")
	// ErrInvalidDelay 水位线延迟为负。
	ErrInvalidDelay = errors.New("window: watermark delay must not be negative")
	// ErrInvalidLateness 迟到容限为负。
	ErrInvalidLateness = errors.New("window: allowed lateness must not be negative")
	// ErrInvalidWindowLimit 未结算窗口上限非正。
	ErrInvalidWindowLimit = errors.New("window: max open windows must be positive")
	// ErrEmptyKey 输入键为空。
	ErrEmptyKey = errors.New("window: key must not be empty")
	// ErrTooManyOpenWindows 同时保留的未结算窗口数超限。
	ErrTooManyOpenWindows = errors.New("window: too many open windows")
)

// Config 为计数器配置，均在 New 时校验。
type Config struct {
	// WindowSize 窗口大小，必须 > 0。窗口按 [n*size, (n+1)*size) 左闭右开划分。
	WindowSize int64
	// WatermarkDelay 水位线延迟，必须 >= 0。
	// 水位线 = 见过的最大事件时间 - WatermarkDelay，只随最大事件时间单调前进。
	WatermarkDelay int64
	// AllowedLateness 迟到容限，必须 >= 0。
	// 窗口在 end+AllowedLateness 时刻仍接受迟到事件并修正结果（边界算容限内）；
	// 水位线严格越过该时刻后窗口被清除，此后该窗口的迟到事件丢弃并计数。
	AllowedLateness int64
	// MaxOpenWindows 同时保留的未结算窗口（按键×窗口）数量上限，必须 > 0。
	MaxOpenWindows int
	// Logger 用于打印输入、输出与判定依据；nil 时使用 slog.Default()。
	Logger *slog.Logger
}

// ResultKind 标识一条输出是窗口首次触发还是迟到修正。
type ResultKind int

const (
	// ResultTriggered 窗口到期首次触发。
	ResultTriggered ResultKind = iota + 1
	// ResultCorrected 迟到事件落在容限内，对已输出结果的修正。
	ResultCorrected
)

// Result 是一条窗口输出。Seq 从 1 开始，按确定的产生顺序编号。
type Result struct {
	Key         string
	WindowStart int64
	WindowEnd   int64
	Count       int64
	Kind        ResultKind
	Seq         int64
}

// WindowState 是一个当前仍保留的（未清除的）按键窗口状态快照。
type WindowState struct {
	Key         string
	WindowStart int64
	WindowEnd   int64
	Count       int64
	Fired       bool
}

// Outcome 是一次 Add 调用对事件本身的处置结果。
type Outcome int

const (
	// OutcomeRejected 输入被整体拒绝（非法键、窗口数超限等），无任何状态改变。
	OutcomeRejected Outcome = iota
	// OutcomeAccepted 事件被接受（计入开窗或触发/修正）。
	OutcomeAccepted
	// OutcomeDropped 事件因迟到超出容限被丢弃，计入 DroppedEvents。
	OutcomeDropped
)

// wkey 标识一个按键窗口。
type wkey struct {
	key   string
	start int64
}

// wstate 是一个按键窗口的内部状态。已触发的窗口在清除前仍保留在表中，
// 以便接收容限内的迟到事件并发出修正。
type wstate struct {
	key   string
	start int64
	end   int64
	count int64
	fired bool
}

// Counter 是并发安全的翻滚窗口计数器。零值不可用，请使用 New 创建。
type Counter struct {
	mu sync.RWMutex

	cfg Config
	log *slog.Logger

	// maxSet 为 false 表示尚未见到任何事件。
	maxSet bool
	maxET  int64
	// watermark 仅随 maxET 单调前进。
	watermark int64

	// windows 包含所有未清除的按键窗口（含已触发、容限内待修正的窗口）。
	windows map[wkey]*wstate

	// outputs 为全部输出的确定顺序；seq 为下一个输出序号。
	outputs []Result
	seq     int64

	dropped int64
}

// New 校验配置并创建计数器；配置非法时返回对应的可区分错误。
func New(cfg Config) (*Counter, error) {
	if cfg.WindowSize <= 0 {
		return nil, ErrInvalidWindowSize
	}
	if cfg.WatermarkDelay < 0 {
		return nil, ErrInvalidDelay
	}
	if cfg.AllowedLateness < 0 {
		return nil, ErrInvalidLateness
	}
	if cfg.MaxOpenWindows <= 0 {
		return nil, ErrInvalidWindowLimit
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Counter{
		cfg:       cfg,
		log:       log,
		watermark: math.MinInt64,
		windows:   make(map[wkey]*wstate),
	}, nil
}

// windowStart 返回事件时间 t 所属窗口的起点（窗口大小 size > 0）。
// 采用余数法避免乘法溢出；对负 t 同样得到左闭右开划分。
// 例如 size=10：t=-1 -> -10（窗口 [-10,0)），t=-10 -> -10（窗口 [-10,0)）。
func windowStart(t, size int64) int64 {
	r := t % size
	if r < 0 {
		r += size
	}
	return t - r
}

// saturatingSub 返回 a-b，溢出时钳制到 int64 边界。
func saturatingSub(a, b int64) int64 {
	if b > 0 && a < math.MinInt64+b {
		return math.MinInt64
	}
	return a - b
}

// saturatingAdd 返回 a+b，溢出时钳制到 int64 边界。
func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Add 送入一个带事件时间的事件。
//
// 处理顺序：校验输入 -> 定位/创建窗口（受 MaxOpenWindows 限制）->
// 推进最大事件时间与水位线 -> 接受或丢弃事件 -> 触发到期窗口 -> 清除过期窗口。
// 被整体拒绝（空键、未结算窗口数超限）时返回对应错误，
// 且不改变水位线、丢弃数与任何已产生输出，计数器之后仍可正常使用；
// 迟到超容限的合法输入返回 OutcomeDropped 且 error 为 nil。
func (c *Counter) Add(key string, eventTime int64) (Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if key == "" {
		c.log.LogAttrs(nil, slog.LevelError, "input rejected: empty key",
			slog.Int64("event_time", eventTime))
		return OutcomeRejected, ErrEmptyKey
	}

	start := windowStart(eventTime, c.cfg.WindowSize)
	end := start + c.cfg.WindowSize
	k := wkey{key: key, start: start}
	gcEnd := saturatingAdd(end, c.cfg.AllowedLateness)

	w, exists := c.windows[k]
	if !exists {
		// 窗口已不在表中只有一种可能：早已触发并在越过容限终点后被清除
		// （或水位线早已越过该窗口的容限终点，使其从未有过存活机会）。
		// 此时按迟到超容限丢弃，绝不重建已关闭窗口。
		// 边界 wm == gcEnd 仍算容限内：建窗后本回合立即触发，之后还可修正。
		late := c.maxSet && gcEnd < c.watermark
		if late {
			c.dropped++
			c.log.LogAttrs(nil, slog.LevelInfo,
				"late event for already-cleared window: dropped",
				slog.String("key", key),
				slog.Int64("event_time", eventTime),
				slog.Int64("window_start", start),
				slog.Int64("window_end", end),
				slog.Int64("gc_threshold", gcEnd),
				slog.Int64("watermark", c.watermark),
				slog.Int64("dropped_total", c.dropped))
			return OutcomeDropped, nil
		}
		// 超限整体拒绝：在任何状态（含水位线）改变之前检查。
		if len(c.windows) >= c.cfg.MaxOpenWindows {
			c.log.LogAttrs(nil, slog.LevelError,
				"input rejected: too many open windows",
				slog.String("key", key),
				slog.Int64("event_time", eventTime),
				slog.Int64("window_start", start),
				slog.Int("open_windows", len(c.windows)),
				slog.Int("limit", c.cfg.MaxOpenWindows))
			return OutcomeRejected, ErrTooManyOpenWindows
		}
		w = &wstate{key: key, start: start, end: end}
		c.windows[k] = w
	}

	// 推进最大事件时间与水位线（水位线单调不减）。
	if !c.maxSet || eventTime > c.maxET {
		c.maxSet = true
		c.maxET = eventTime
	}
	newWM := saturatingSub(c.maxET, c.cfg.WatermarkDelay)
	if newWM > c.watermark {
		c.watermark = newWM
	}
	wm := c.watermark

	c.log.LogAttrs(nil, slog.LevelInfo, "input accepted",
		slog.String("key", key),
		slog.Int64("event_time", eventTime),
		slog.Int64("window_start", start),
		slog.Int64("window_end", end),
		slog.Int64("watermark", wm))

	// 迟到判定：窗口已触发，且水位线严格越过 end+AllowedLateness -> 丢弃。
	// 边界（watermark == end+lateness）算容限内，接受并修正。
	outcome := OutcomeAccepted
	if !w.fired {
		w.count++
		c.log.LogAttrs(nil, slog.LevelInfo, "event counted in open window",
			slog.String("key", key), slog.Int64("window_start", start),
			slog.Int64("new_count", w.count))
	} else if wm <= gcEnd {
		w.count++
		c.seq++
		res := Result{
			Key:         key,
			WindowStart: start,
			WindowEnd:   end,
			Count:       w.count,
			Kind:        ResultCorrected,
			Seq:         c.seq,
		}
		c.outputs = append(c.outputs, res)
		c.log.LogAttrs(nil, slog.LevelInfo,
			"late event within allowed lateness: corrected output emitted",
			slog.String("key", key),
			slog.Int64("event_time", eventTime),
			slog.Int64("window_start", start),
			slog.Int64("window_end", end),
			slog.Int64("gc_threshold", gcEnd),
			slog.Int64("watermark", wm),
			slog.Int64("corrected_count", w.count),
			slog.Int64("seq", c.seq))
	} else {
		outcome = OutcomeDropped
		c.dropped++
		c.log.LogAttrs(nil, slog.LevelInfo,
			"late event beyond allowed lateness: dropped",
			slog.String("key", key),
			slog.Int64("event_time", eventTime),
			slog.Int64("window_start", start),
			slog.Int64("window_end", end),
			slog.Int64("gc_threshold", gcEnd),
			slog.Int64("watermark", wm),
			slog.Int64("dropped_total", c.dropped))
	}

	// 触发所有到期窗口：end <= watermark。每次选 end 最小、再按 key 最小者，
	// 保证同水位线下输出顺序确定、可复现。
	c.fireDueWindows(wm)

	// 清除已触发且 gcEnd < watermark 的窗口（同样按确定性顺序逐个清除）。
	c.gcExpiredWindows(wm)

	return outcome, nil
}

// fireDueWindows 触发所有 end <= wm 且尚未触发的窗口。
func (c *Counter) fireDueWindows(wm int64) {
	for {
		var best *wstate
		for _, w := range c.windows {
			if w.fired || w.end > wm {
				continue
			}
			if best == nil || w.end < best.end || (w.end == best.end && w.key < best.key) {
				best = w
			}
		}
		if best == nil {
			return
		}
		best.fired = true
		c.seq++
		res := Result{
			Key:         best.key,
			WindowStart: best.start,
			WindowEnd:   best.end,
			Count:       best.count,
			Kind:        ResultTriggered,
			Seq:         c.seq,
		}
		c.outputs = append(c.outputs, res)
		c.log.LogAttrs(nil, slog.LevelInfo, "window fired",
			slog.String("key", best.key),
			slog.Int64("window_start", best.start),
			slog.Int64("window_end", best.end),
			slog.Int64("count", best.count),
			slog.Int64("watermark", wm),
			slog.Int64("seq", c.seq))
	}
}

// gcExpiredWindows 清除已触发且 end+AllowedLateness < wm 的窗口。
func (c *Counter) gcExpiredWindows(wm int64) {
	var dead []wkey
	for k, w := range c.windows {
		if !w.fired {
			continue
		}
		if saturatingAdd(w.end, c.cfg.AllowedLateness) < wm {
			dead = append(dead, k)
		}
	}
	// 确定性顺序：window_start 升序、再按 key 升序，仅影响日志可读性。
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].start != dead[j].start {
			return dead[i].start < dead[j].start
		}
		return dead[i].key < dead[j].key
	})
	for _, k := range dead {
		delete(c.windows, k)
		c.log.LogAttrs(nil, slog.LevelInfo, "window cleared after lateness horizon",
			slog.String("key", k.key),
			slog.Int64("window_start", k.start),
			slog.Int64("watermark", wm))
	}
}

// Watermark 返回当前水位线；尚未见到任何事件时返回 math.MinInt64。
func (c *Counter) Watermark() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.watermark
}

// MaxEventTime 返回见过的最大事件时间；尚未见到事件时第二个返回值为 false。
func (c *Counter) MaxEventTime() (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxET, c.maxSet
}

// DroppedEvents 返回因迟到超出容限而被丢弃的事件总数。
func (c *Counter) DroppedEvents() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dropped
}

// Results 返回截至当前全部输出（含首次触发与迟到修正）的拷贝，
// 顺序即输出的确定顺序。多次并发只读得到逐字段一致的结果。
func (c *Counter) Results() []Result {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Result, len(c.outputs))
	copy(out, c.outputs)
	return out
}

// ActiveWindows 返回当前仍保留的全部按键窗口状态快照，
// 按 (WindowStart, Key) 升序排序，保证可复现。
func (c *Counter) ActiveWindows() []WindowState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]WindowState, 0, len(c.windows))
	for _, w := range c.windows {
		out = append(out, WindowState{
			Key:         w.key,
			WindowStart: w.start,
			WindowEnd:   w.end,
			Count:       w.count,
			Fired:       w.fired,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowStart != out[j].WindowStart {
			return out[i].WindowStart < out[j].WindowStart
		}
		return out[i].Key < out[j].Key
	})
	return out
}
