// Package window 提供带三档触发器（早触发 / 准点 / 迟到）的翻滚窗口计数器。
//
// 窗口按固定大小左闭右开切分（支持负时间戳）；水位线 = 已见最大事件时间 - Delay，
// 只进不退。触发规则：
//   - 早触发：水位线未越过窗口右边界时，计数每达到 EarlyEvery 的整数倍输出一条
//     中间快照，计数不清零；
//   - 准点：水位线越过窗口右边界（watermark >= end）时输出最终计数，每窗口至多一次；
//   - 迟到：准点之后、水位线未越过 end+LateLimit 之前到达的事件被接受，
//     先撤回旧值再发新值；超过上限的事件被丢弃并计数；
//   - 清除：水位线越过 end+LateLimit 后窗口状态被清除。
package window

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// TriggerType 触发类型。
type TriggerType string

const (
	TriggerEarly  TriggerType = "EARLY"
	TriggerOnTime TriggerType = "ON_TIME"
	TriggerLate   TriggerType = "LATE"
)

// ChangeKind 变更日志条目种类。
type ChangeKind string

const (
	KindUpsert  ChangeKind = "UPSERT"
	KindRetract ChangeKind = "RETRACT"
)

// Event 输入事件，Time 为事件时间（可为负）。
type Event struct {
	Key  string
	Time int64
}

// Change 一条变更日志。Seq 为全局单调序号，保证输出可复现。
type Change struct {
	Seq     int64
	Key     string
	Start   int64
	End     int64
	Trigger TriggerType
	Kind    ChangeKind
	Value   int64
}

// Config 计数器配置。
type Config struct {
	Size       int64 // 窗口大小，必须 > 0
	EarlyEvery int64 // 早触发阈值（计数倍数），必须 > 0
	Delay      int64 // 水位线延迟，必须 >= 0
	LateLimit  int64 // 迟到上限，必须 >= 0
	MaxWindows int   // 最大未清除窗口数，必须 > 0
}

// ConfigError 非法配置错误。
type ConfigError struct {
	Field  string
	Detail string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("window: 非法配置字段 %s: %s", e.Field, e.Detail)
}

// RejectReason 批次被拒绝的原因，可区分。
type RejectReason string

const (
	ReasonEmptyKey       RejectReason = "EMPTY_KEY"
	ReasonTooManyWindows RejectReason = "TOO_MANY_WINDOWS"
)

// RejectError 批次整体拒绝错误。
type RejectError struct {
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("window: 批次被拒绝(%s): %s", e.Reason, e.Detail)
}

// subSat 饱和减法，防止 watermark 计算溢出。
func subSat(a, b int64) int64 {
	if b > 0 && a < math.MinInt64+b {
		return math.MinInt64
	}
	if b < 0 && a > math.MaxInt64+b {
		return math.MaxInt64
	}
	return a - b
}

// addSat 饱和加法，防止窗口右边界 / 清除时刻计算溢出。
func addSat(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

// WindowView 单个未清除窗口的只读视图。
type WindowView struct {
	Key         string
	Start       int64
	End         int64
	Count       int64
	OnTimeFired bool
}

// View 计数器某一时刻的一致性快照。
type View struct {
	HasEvents    bool
	MaxEventTime int64
	Watermark    int64
	Dropped      int64
	Changes      int64
	Windows      []WindowView
}

type windowID struct {
	key   string
	start int64
}

type windowState struct {
	count       int64
	onTimeFired bool
}

// Counter 翻滚窗口计数器，所有方法可并发调用。
type Counter struct {
	mu sync.RWMutex

	cfg       Config
	hasEvents bool
	maxTime   int64
	watermark int64
	dropped   int64
	seq       int64

	windows  map[windowID]*windowState
	accepted []Event
	changes  []Change
}

// NewCounter 校验配置并创建计数器。
func NewCounter(cfg Config) (*Counter, error) {
	if cfg.Size <= 0 {
		return nil, &ConfigError{Field: "Size", Detail: "窗口大小必须为正整数"}
	}
	if cfg.EarlyEvery <= 0 {
		return nil, &ConfigError{Field: "EarlyEvery", Detail: "早触发阈值必须为正整数"}
	}
	if cfg.Delay < 0 {
		return nil, &ConfigError{Field: "Delay", Detail: "水位线延迟不能为负"}
	}
	if cfg.LateLimit < 0 {
		return nil, &ConfigError{Field: "LateLimit", Detail: "迟到上限不能为负"}
	}
	if cfg.MaxWindows <= 0 {
		return nil, &ConfigError{Field: "MaxWindows", Detail: "最大未清除窗口数必须为正整数"}
	}
	return &Counter{
		cfg:       cfg,
		watermark: math.MinInt64, // 无任何事件时水位线视为负无穷
		windows:   make(map[windowID]*windowState),
	}, nil
}

// AddBatch 原子地应用一批事件：任一条被拒则整批不生效、状态不变。
// 返回本批产生的变更日志（按 Seq 递增）。
func (c *Counter) AddBatch(events []Event) ([]Change, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(events) == 0 {
		return nil, nil
	}
	// 第一遍只做校验与窗口数模拟，不触碰真实状态；
	// 有任何拒绝则整批不生效。
	if err := c.precheck(events); err != nil {
		return nil, err
	}
	base := len(c.changes)
	for _, ev := range events {
		c.applyOne(ev)
	}
	out := make([]Change, len(c.changes)-base)
	copy(out, c.changes[base:])
	return out, nil
}

// winStart 计算事件所属窗口的左边界（左闭右开，支持负时间戳）。
func (c *Counter) winStart(ts int64) int64 {
	r := ts % c.cfg.Size
	if r < 0 {
		r += c.cfg.Size
	}
	return ts - r
}

// precheck 预检整批事件：空键拒绝；模拟水位线推进与窗口生命周期，
// 未清除窗口数将超上限时拒绝。不修改计数器真实状态。
func (c *Counter) precheck(events []Event) error {
	live := make(map[windowID]struct{}, len(c.windows))
	for id := range c.windows {
		live[id] = struct{}{}
	}
	maxTime, has := c.maxTime, c.hasEvents
	for i, ev := range events {
		if ev.Key == "" {
			return &RejectError{
				Reason: ReasonEmptyKey,
				Detail: fmt.Sprintf("第 %d 条事件的键为空", i),
			}
		}
		if !has || ev.Time > maxTime {
			maxTime, has = ev.Time, true
		}
		wm := subSat(maxTime, c.cfg.Delay)
		start := c.winStart(ev.Time)
		end := addSat(start, c.cfg.Size)
		if wm >= addSat(end, c.cfg.LateLimit) {
			continue // 超迟到上限被丢弃，不占用窗口
		}
		id := windowID{key: ev.Key, start: start}
		if _, ok := live[id]; !ok {
			if len(live)+1 > c.cfg.MaxWindows {
				return &RejectError{
					Reason: ReasonTooManyWindows,
					Detail: fmt.Sprintf("第 %d 条事件将创建窗口 [%d,%d)，未清除窗口数将达 %d，超过上限 %d",
						i, start, end, len(live)+1, c.cfg.MaxWindows),
				}
			}
			live[id] = struct{}{}
		}
		// 模拟水位线推进带来的窗口清除。
		for old := range live {
			oldEnd := addSat(old.start, c.cfg.Size)
			if wm >= addSat(oldEnd, c.cfg.LateLimit) {
				delete(live, old)
			}
		}
	}
	return nil
}

// applyOne 应用单条事件（调用前已通过 precheck）。
func (c *Counter) applyOne(ev Event) {
	// 水位线只进不退：由已见最大事件时间减去延迟得到。
	if !c.hasEvents || ev.Time > c.maxTime {
		c.maxTime, c.hasEvents = ev.Time, true
	}
	if wm := subSat(c.maxTime, c.cfg.Delay); wm > c.watermark {
		c.watermark = wm
	}

	start := c.winStart(ev.Time)
	end := addSat(start, c.cfg.Size)
	clearAt := addSat(end, c.cfg.LateLimit)

	if c.watermark >= clearAt {
		c.dropped++ // 超迟到上限：丢弃并计数
		return
	}
	c.accepted = append(c.accepted, ev)

	id := windowID{key: ev.Key, start: start}
	st, ok := c.windows[id]
	if !ok {
		st = &windowState{}
		c.windows[id] = st
	}

	switch {
	case st.onTimeFired:
		// 迟到事件：先撤回旧值，再发新值。
		c.emit(ev.Key, start, end, TriggerLate, KindRetract, st.count)
		st.count++
		c.emit(ev.Key, start, end, TriggerLate, KindUpsert, st.count)
	default:
		st.count++
		// 早触发：水位线越过右边界前，计数达到阈值的整数倍，
		// 输出中间快照且计数不清零。
		if c.watermark < end && st.count%c.cfg.EarlyEvery == 0 {
			c.emit(ev.Key, start, end, TriggerEarly, KindUpsert, st.count)
		}
	}
	c.sweep()
}

// sweep 按确定性顺序处理准点触发与状态清除。
func (c *Counter) sweep() {
	ids := make([]windowID, 0, len(c.windows))
	for id := range c.windows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].start != ids[j].start {
			return ids[i].start < ids[j].start
		}
		return ids[i].key < ids[j].key
	})
	for _, id := range ids {
		st := c.windows[id]
		end := addSat(id.start, c.cfg.Size)
		// 准点触发：水位线越过右边界，输出最终计数，每窗口至多一次。
		if !st.onTimeFired && c.watermark >= end {
			st.onTimeFired = true
			c.emit(id.key, id.start, end, TriggerOnTime, KindUpsert, st.count)
		}
		// 水位线越过右边界加迟到上限后，清除窗口状态。
		if c.watermark >= addSat(end, c.cfg.LateLimit) {
			delete(c.windows, id)
		}
	}
}

// emit 追加一条变更日志，Seq 全局单调递增。
func (c *Counter) emit(key string, start, end int64, trig TriggerType, kind ChangeKind, value int64) {
	c.seq++
	c.changes = append(c.changes, Change{
		Seq:     c.seq,
		Key:     key,
		Start:   start,
		End:     end,
		Trigger: trig,
		Kind:    kind,
		Value:   value,
	})
}

// View 返回当前一致性快照，可并发调用。
func (c *Counter) View() View {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := View{
		HasEvents:    c.hasEvents,
		MaxEventTime: c.maxTime,
		Watermark:    c.watermark,
		Dropped:      c.dropped,
		Changes:      int64(len(c.changes)),
		Windows:      make([]WindowView, 0, len(c.windows)),
	}
	for id, st := range c.windows {
		v.Windows = append(v.Windows, WindowView{
			Key:         id.key,
			Start:       id.start,
			End:         addSat(id.start, c.cfg.Size),
			Count:       st.count,
			OnTimeFired: st.onTimeFired,
		})
	}
	// 排序保证并发读取得到的视图逐字段可比较、可复现。
	sort.Slice(v.Windows, func(i, j int) bool {
		if v.Windows[i].Key != v.Windows[j].Key {
			return v.Windows[i].Key < v.Windows[j].Key
		}
		return v.Windows[i].Start < v.Windows[j].Start
	})
	return v
}

// Dropped 返回被丢弃（超迟到上限）的事件总数，可并发调用。
func (c *Counter) Dropped() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dropped
}

// SelfCheck 用已接受事件日志批量重算各窗口计数，并与窗口状态及
// 变更日志的最新值逐窗口核对，可并发调用。
func (c *Counter) SelfCheck() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 1. 批量重算：按 (键, 窗口) 统计已接受事件数。
	totals := make(map[windowID]int64)
	for _, ev := range c.accepted {
		totals[windowID{key: ev.Key, start: c.winStart(ev.Time)}]++
	}

	// 2. 未清除窗口的计数必须与重算结果一致。
	for id, st := range c.windows {
		if got := totals[id]; got != st.count {
			return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) 状态计数 %d != 重算值 %d",
				id.key, id.start, addSat(id.start, c.cfg.Size), st.count, got)
		}
	}

	// 3. 变更日志逐窗口核对：UPSERT 值单调不减（仅准点最终值允许与
	// 上一条早触发快照相等）；RETRACT 必须等于上一条 UPSERT 的值；
	// 最新 UPSERT 必须等于重算总数。
	lastUpsert := make(map[windowID]int64)
	lastTrigger := make(map[windowID]TriggerType)
	hasUpsert := make(map[windowID]bool)
	for _, ch := range c.changes {
		id := windowID{key: ch.Key, start: ch.Start}
		switch ch.Kind {
		case KindUpsert:
			if hasUpsert[id] {
				if ch.Value < lastUpsert[id] {
					return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) UPSERT 值 %d 出现回退（上一值 %d）",
						ch.Key, ch.Start, ch.End, ch.Value, lastUpsert[id])
				}
				if ch.Value == lastUpsert[id] && ch.Trigger != TriggerOnTime {
					return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) %s 值 %d 与上一值重复（仅准点最终值允许持平）",
						ch.Key, ch.Start, ch.End, ch.Trigger, ch.Value)
				}
				if ch.Trigger == TriggerOnTime && lastTrigger[id] == TriggerOnTime {
					return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) 准点触发重复", ch.Key, ch.Start, ch.End)
				}
			}
			lastUpsert[id], hasUpsert[id] = ch.Value, true
			lastTrigger[id] = ch.Trigger
		case KindRetract:
			if !hasUpsert[id] || ch.Value != lastUpsert[id] {
				return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) RETRACT 值 %d 与最新 UPSERT 不匹配",
					ch.Key, ch.Start, ch.End, ch.Value)
			}
		}
	}
	for id, v := range lastUpsert {
		if st, ok := c.windows[id]; ok && !st.onTimeFired {
			// 准点未触发：最近一条是早触发快照，只需不超过当前计数。
			if v > totals[id] {
				return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) 早触发快照值 %d 超过重算值 %d",
					id.key, id.start, addSat(id.start, c.cfg.Size), v, totals[id])
			}
			continue
		}
		// 准点已触发或状态已清除：最新输出值必须等于重算总数。
		if totals[id] != v {
			return fmt.Errorf("自检失败: 窗口 %s@[%d,%d) 最新输出值 %d != 重算值 %d",
				id.key, id.start, addSat(id.start, c.cfg.Size), v, totals[id])
		}
	}

	// 4. 水位线不变式：watermark == maxEventTime - Delay（饱和）。
	if c.hasEvents {
		if want := subSat(c.maxTime, c.cfg.Delay); c.watermark != want {
			return fmt.Errorf("自检失败: 水位线 %d != 最大事件时间 %d - 延迟 %d",
				c.watermark, c.maxTime, c.cfg.Delay)
		}
	}
	return nil
}
