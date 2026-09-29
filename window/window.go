// Package window 实现带早触发、准点触发与迟到触发三档触发器的翻滚窗口计数。
package window

import (
	"fmt"
	"sync"
)

// TriggerType 变更日志的触发类型。
type TriggerType string

const (
	// TriggerEarly 早触发：水位线越过窗口右边界前，计数每达到 EarlyEvery 的整数倍输出一次中间快照，计数不清零。
	TriggerEarly TriggerType = "EARLY"
	// TriggerOnTime 准点触发：水位线达到或越过窗口右边界时输出最终计数，每窗口至多一次。
	TriggerOnTime TriggerType = "ON_TIME"
	// TriggerLateRetract 迟到撤回：准点触发后接受迟到事件时，先撤回旧值。
	TriggerLateRetract TriggerType = "LATE_RETRACT"
	// TriggerLateUpdate 迟到更新：撤回旧值后发出新值。
	TriggerLateUpdate TriggerType = "LATE_UPDATE"
)

// Event 输入事件。
type Event struct {
	Key       string
	Timestamp int64
}

// Change 一条变更日志。
type Change struct {
	Seq         int64
	Key         string
	WindowStart int64
	WindowEnd   int64
	Type        TriggerType
	Count       int64
}

// Config 引擎配置。
type Config struct {
	WindowSize      int64 // 窗口固定大小（左闭右开），必须 > 0
	EarlyEvery      int64 // 早触发阈值（计数整数倍），必须 > 0
	WatermarkDelay  int64 // 水位线 = 已见最大事件时间 - WatermarkDelay，必须 >= 0
	AllowedLateness int64 // 迟到上限，必须 >= 0
	MaxOpenWindows  int   // 未清除窗口数上限，必须 > 0
}

// Validate 校验配置合法性。
func (c Config) Validate() error {
	if c.WindowSize <= 0 {
		return &RejectError{Reason: ReasonInvalidConfig, Detail: fmt.Sprintf("WindowSize 必须 > 0，实际 %d", c.WindowSize)}
	}
	if c.EarlyEvery <= 0 {
		return &RejectError{Reason: ReasonInvalidConfig, Detail: fmt.Sprintf("EarlyEvery 必须 > 0，实际 %d", c.EarlyEvery)}
	}
	if c.WatermarkDelay < 0 {
		return &RejectError{Reason: ReasonInvalidConfig, Detail: fmt.Sprintf("WatermarkDelay 必须 >= 0，实际 %d", c.WatermarkDelay)}
	}
	if c.AllowedLateness < 0 {
		return &RejectError{Reason: ReasonInvalidConfig, Detail: fmt.Sprintf("AllowedLateness 必须 >= 0，实际 %d", c.AllowedLateness)}
	}
	if c.MaxOpenWindows <= 0 {
		return &RejectError{Reason: ReasonInvalidConfig, Detail: fmt.Sprintf("MaxOpenWindows 必须 > 0，实际 %d", c.MaxOpenWindows)}
	}
	return nil
}

// RejectReason 可区分的拒绝原因。
type RejectReason string

const (
	ReasonInvalidConfig  RejectReason = "INVALID_CONFIG"
	ReasonEmptyKey       RejectReason = "EMPTY_KEY"
	ReasonTooManyWindows RejectReason = "TOO_MANY_WINDOWS"
)

// RejectError 整体拒绝错误，Reason 可区分原因。
type RejectError struct {
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

// WindowKey 标识一个窗口实例。
type WindowKey struct {
	Key   string
	Start int64
}

// WindowView 单个窗口的只读视图。
type WindowView struct {
	Key         string
	Start       int64
	End         int64
	Count       int64
	OnTimeFired bool
}

// Snapshot 引擎某一时刻的一致性视图。
type Snapshot struct {
	Watermark    int64
	MaxEventTime int64
	HasEvent     bool
	OpenWindows  map[WindowKey]WindowView
	Dropped      int64
	Changelog    []Change
}

// windowState 内部窗口状态。
type windowState struct {
	key         string
	start       int64
	count       int64
	onTimeFired bool
}

// state 引擎可变状态（克隆用于批量原子性）。
type state struct {
	maxEventTime int64
	hasEvent     bool
	watermark    int64
	windows      map[WindowKey]*windowState
	dropped      int64
	seq          int64
	changelog    []Change
}

// Engine 翻滚窗口计数引擎，并发安全。
type Engine struct {
	mu  sync.RWMutex
	cfg Config
	st  state
}

// NewEngine 创建引擎，配置非法时返回 ReasonInvalidConfig 错误。
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{
		cfg: cfg,
		st: state{
			windows: make(map[WindowKey]*windowState),
		},
	}, nil
}

// floorDiv 数学向下取整除法，支持负数。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// clone 深拷贝可变状态，用于批量原子应用。
func (s *state) clone() state {
	out := state{
		maxEventTime: s.maxEventTime,
		hasEvent:     s.hasEvent,
		watermark:    s.watermark,
		windows:      make(map[WindowKey]*windowState, len(s.windows)),
		dropped:      s.dropped,
		seq:          s.seq,
		changelog:    make([]Change, len(s.changelog)),
	}
	for k, w := range s.windows {
		cp := *w
		out.windows[k] = &cp
	}
	copy(out.changelog, s.changelog)
	return out
}

// emit 追加一条变更日志。
func (s *state) emit(key string, start, end int64, typ TriggerType, count int64) {
	s.seq++
	s.changelog = append(s.changelog, Change{
		Seq:         s.seq,
		Key:         key,
		WindowStart: start,
		WindowEnd:   end,
		Type:        typ,
		Count:       count,
	})
}

// Ingest 原子地应用一批事件：任一条被拒则整批不生效、状态不变。
// 返回本批产生的变更日志。
func (e *Engine) Ingest(events []Event) ([]Change, error) {
	// 先整体校验空键，不触碰状态。
	for i, ev := range events {
		if ev.Key == "" {
			return nil, &RejectError{Reason: ReasonEmptyKey, Detail: fmt.Sprintf("第 %d 条事件键为空", i)}
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 在克隆状态上应用，出错即丢弃，保证整批原子性。
	work := e.st.clone()
	baseSeq := work.seq
	for _, ev := range events {
		if err := e.applyOne(&work, ev); err != nil {
			return nil, err
		}
	}
	e.st = work
	return append([]Change(nil), e.st.changelog[baseSeq:]...), nil
}

// applyOne 在给定状态上应用单条事件。
func (e *Engine) applyOne(st *state, ev Event) error {
	cfg := e.cfg
	start := floorDiv(ev.Timestamp, cfg.WindowSize) * cfg.WindowSize
	end := start + cfg.WindowSize
	wk := WindowKey{Key: ev.Key, Start: start}

	// 迟到判定：以本条事件到达前的水位线为准。
	if st.hasEvent && st.watermark >= end {
		if st.watermark >= end+cfg.AllowedLateness {
			// 超出迟到上限：丢弃并计数。事件仍属“已见”，可推进水位线。
			st.dropped++
			e.advanceWatermark(st, ev.Timestamp)
			return nil
		}
		w, ok := st.windows[wk]
		if !ok {
			// 窗口从未有过事件：补建状态，准点时机已过，标记已触发。
			if len(st.windows) >= cfg.MaxOpenWindows {
				return &RejectError{
					Reason: ReasonTooManyWindows,
					Detail: fmt.Sprintf("未清除窗口数已达上限 %d，无法为键 %q 的迟到窗口 [%d,%d) 分配状态", cfg.MaxOpenWindows, ev.Key, start, end),
				}
			}
			w = &windowState{key: ev.Key, start: start, onTimeFired: true}
			st.windows[wk] = w
		}
		// 未超迟到上限：接受，先撤回旧值再发新值。
		old := w.count
		w.count++
		st.emit(ev.Key, start, end, TriggerLateRetract, old)
		st.emit(ev.Key, start, end, TriggerLateUpdate, w.count)
		e.advanceWatermark(st, ev.Timestamp)
		return nil
	}

	// 正常路径：计入窗口。
	w, ok := st.windows[wk]
	if !ok {
		if len(st.windows) >= cfg.MaxOpenWindows {
			return &RejectError{
				Reason: ReasonTooManyWindows,
				Detail: fmt.Sprintf("未清除窗口数已达上限 %d，无法为键 %q 的窗口 [%d,%d) 分配状态", cfg.MaxOpenWindows, ev.Key, start, end),
			}
		}
		w = &windowState{key: ev.Key, start: start}
		st.windows[wk] = w
	}
	w.count++

	// 早触发：计数每达到阈值的整数倍输出一次快照，计数不清零。
	if w.count%cfg.EarlyEvery == 0 {
		st.emit(ev.Key, start, end, TriggerEarly, w.count)
	}

	e.advanceWatermark(st, ev.Timestamp)
	return nil
}

// advanceWatermark 更新已见最大事件时间并推进水位线（只进不退），
// 随后执行准点触发与状态清除。
func (e *Engine) advanceWatermark(st *state, ts int64) {
	first := !st.hasEvent
	if first || ts > st.maxEventTime {
		st.maxEventTime = ts
		st.hasEvent = true
	}
	wm := st.maxEventTime - e.cfg.WatermarkDelay
	if first || wm > st.watermark {
		st.watermark = wm
	}
	e.fireAndClean(st)
}

// fireAndClean 对水位线达到右边界的窗口做准点触发（每窗口至多一次），
// 并清除水位线越过右边界 + 迟到上限的窗口状态。
func (e *Engine) fireAndClean(st *state) {
	cfg := e.cfg
	for wk, w := range st.windows {
		end := wk.Start + cfg.WindowSize
		if !w.onTimeFired && st.watermark >= end {
			w.onTimeFired = true
			st.emit(w.key, wk.Start, end, TriggerOnTime, w.count)
		}
		if st.watermark >= end+cfg.AllowedLateness {
			delete(st.windows, wk)
		}
	}
}

// Snapshot 返回引擎当前状态的一致性深拷贝视图，可并发调用。
func (e *Engine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap := Snapshot{
		Watermark:    e.st.watermark,
		MaxEventTime: e.st.maxEventTime,
		HasEvent:     e.st.hasEvent,
		OpenWindows:  make(map[WindowKey]WindowView, len(e.st.windows)),
		Dropped:      e.st.dropped,
		Changelog:    append([]Change(nil), e.st.changelog...),
	}
	for wk, w := range e.st.windows {
		snap.OpenWindows[wk] = WindowView{
			Key:         w.key,
			Start:       wk.Start,
			End:         wk.Start + e.cfg.WindowSize,
			Count:       w.count,
			OnTimeFired: w.onTimeFired,
		}
	}
	return snap
}

// Dropped 返回被丢弃的迟到事件总数，可并发调用。
func (e *Engine) Dropped() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.dropped
}

// FinalCounts 返回每个已知窗口的最终计数，可并发调用：
// 仍未清除的窗口取当前实时计数，已清除的窗口取变更日志最终取值。
func (e *Engine) FinalCounts() []FinalCount {
	snap := e.Snapshot()
	merged := make(map[WindowKey]FinalCount)
	order := make([]WindowKey, 0)
	for _, f := range FinalCountsFromChangelog(snap.Changelog) {
		wk := WindowKey{Key: f.Key, Start: f.WindowStart}
		if _, ok := merged[wk]; !ok {
			order = append(order, wk)
		}
		merged[wk] = f
	}
	for wk, w := range snap.OpenWindows {
		if _, ok := merged[wk]; !ok {
			order = append(order, wk)
		}
		merged[wk] = FinalCount{Key: w.Key, WindowStart: w.Start, WindowEnd: w.End, Count: w.Count}
	}
	out := make([]FinalCount, 0, len(order))
	for _, wk := range order {
		out = append(out, merged[wk])
	}
	return out
}

// SelfCheck 校验内部不变量，可并发调用；发现不一致时返回描述性错误。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := &e.st
	cfg := e.cfg

	if st.hasEvent && st.watermark > st.maxEventTime-cfg.WatermarkDelay {
		return fmt.Errorf("水位线 %d 超过 已见最大事件时间 %d - 延迟 %d", st.watermark, st.maxEventTime, cfg.WatermarkDelay)
	}
	if st.dropped < 0 {
		return fmt.Errorf("丢弃计数为负: %d", st.dropped)
	}
	for wk, w := range st.windows {
		if w.count <= 0 {
			return fmt.Errorf("窗口 %+v 计数非正: %d", wk, w.count)
		}
		end := wk.Start + cfg.WindowSize
		if st.watermark >= end+cfg.AllowedLateness {
			return fmt.Errorf("窗口 %+v 应已清除（水位线 %d >= 右边界 %d + 迟到上限 %d）", wk, st.watermark, end, cfg.AllowedLateness)
		}
		if !w.onTimeFired && st.watermark >= end {
			return fmt.Errorf("窗口 %+v 应已准点触发（水位线 %d >= 右边界 %d）", wk, st.watermark, end)
		}
	}

	// 变更日志结构校验：逐窗口检查触发序列合法。
	type winLog struct {
		onTime    int
		lastEarly int64
		lastType  TriggerType
		expectOld int64
	}
	logs := make(map[WindowKey]*winLog)
	for _, c := range st.changelog {
		wk := WindowKey{Key: c.Key, Start: c.WindowStart}
		wl := logs[wk]
		if wl == nil {
			wl = &winLog{}
			logs[wk] = wl
		}
		if c.WindowEnd != c.WindowStart+cfg.WindowSize {
			return fmt.Errorf("变更 #%d 窗口边界非法: [%d,%d)", c.Seq, c.WindowStart, c.WindowEnd)
		}
		switch c.Type {
		case TriggerEarly:
			if wl.lastType != "" && wl.lastType != TriggerEarly {
				return fmt.Errorf("变更 #%d 早触发出现在准点/迟到阶段之后", c.Seq)
			}
			if c.Count%cfg.EarlyEvery != 0 || c.Count <= wl.lastEarly {
				return fmt.Errorf("变更 #%d 早触发计数 %d 非阈值整数倍或未递增", c.Seq, c.Count)
			}
			wl.lastEarly = c.Count
			wl.expectOld = c.Count
		case TriggerOnTime:
			wl.onTime++
			if wl.onTime > 1 {
				return fmt.Errorf("变更 #%d 窗口 %+v 准点触发超过一次", c.Seq, wk)
			}
			if wl.lastType != "" && wl.lastType != TriggerEarly {
				return fmt.Errorf("变更 #%d 准点触发前置记录非法", c.Seq)
			}
			wl.expectOld = c.Count
		case TriggerLateRetract:
			if wl.lastType != TriggerOnTime && wl.lastType != TriggerLateUpdate && wl.lastType != "" {
				return fmt.Errorf("变更 #%d 迟到撤回出现在准点触发之前", c.Seq)
			}
			if c.Count != wl.expectOld {
				return fmt.Errorf("变更 #%d 撤回值 %d 与上一取值 %d 不一致", c.Seq, c.Count, wl.expectOld)
			}
		case TriggerLateUpdate:
			if wl.lastType != TriggerLateRetract {
				return fmt.Errorf("变更 #%d 迟到更新缺少前置撤回", c.Seq)
			}
			if c.Count != wl.expectOld+1 {
				return fmt.Errorf("变更 #%d 迟到更新值 %d 应为 %d", c.Seq, c.Count, wl.expectOld+1)
			}
			wl.expectOld = c.Count
		default:
			return fmt.Errorf("变更 #%d 未知触发类型 %q", c.Seq, c.Type)
		}
		wl.lastType = c.Type
	}
	return nil
}
