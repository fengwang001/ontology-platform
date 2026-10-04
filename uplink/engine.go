package uplink

import (
	"errors"
	"sync"

	"ontology/filter"
	"ontology/profile"
)

var (
	ErrInvalid   = errors.New("telemetry: invalid argument")
	ErrClockBack = errors.New("telemetry: clock moved backwards")
	ErrNoPoint   = errors.New("telemetry: point not found")
)

type Reason string

const (
	First     Reason = "First"
	Change    Reason = "Change"
	Trailing  Reason = "Trailing"
	Heartbeat Reason = "Heartbeat"
	Recover   Reason = "Recover"
)

type Kind string

const (
	KindReport  Kind = "Report"
	KindFault   Kind = "Fault"
	KindRecover Kind = "Recover"
)

type Event struct {
	Kind   Kind
	Point  string
	At     int64
	V      int64
	Reason Reason
}

type Engine struct {
	mu sync.Mutex

	profiles *profile.Catalog
	states   map[string]*filter.State

	// 每测点在堆中的两条条目引用；nil 表示当前不存在。
	pendingItem   map[string]*entry
	heartbeatItem map[string]*entry
	events        eventHeap

	used      map[int64]int64 // 窗口序号 -> 已用额度
	throttled int64
	popped    int64

	// 最近一次 tickLocked 的统计，供 popped 不变量验证。
	lastPopped    int64
	lastReports   int64
	lastThrottles int64

	clock    int64
	wn, u, q int64
}

// TickStats 是一次 Tick 处理的统计：弹出条目数、发出数与推迟数。
type TickStats struct {
	Popped, Reports, Throttled int64
}

func NewEngine(wn, u, q int64) (*Engine, error) {
	if wn < 1 || wn > profile.MaxInterval || u < 1 || u > 10_000 || q < 1 || q > 100 {
		return nil, ErrInvalid
	}
	return &Engine{
		profiles:      profile.NewCatalog(),
		states:        map[string]*filter.State{},
		pendingItem:   map[string]*entry{},
		heartbeatItem: map[string]*entry{},
		used:          map[int64]int64{},
		wn:            wn,
		u:             u,
		q:             q,
	}, nil
}

func validValue(v int64) bool { return v >= -profile.MaxValue && v <= profile.MaxValue }

// checkTime 完成非法参数与时钟回退两级判定。
// t 越界或回退幅度超过 1e7 为 ErrInvalid；其余 t<clock 为 ErrClockBack；
// t==clock 合法（同一时刻可连续登记/热更新）。
func (e *Engine) checkTime(t int64) error {
	if t < 0 || t > profile.MaxTime {
		return ErrInvalid
	}
	if t < e.clock {
		if e.clock-t > profile.MaxStep {
			return ErrInvalid
		}
		return ErrClockBack
	}
	return nil
}

// refresh 依据当前状态与参数重算某测点的两条堆条目。
func (e *Engine) refresh(name string) {
	p, ok := e.profiles.Get(name)
	if !ok {
		return
	}
	s := e.states[name]

	// 先摘除旧对象（若仍在堆中），再按新键值全新压入，避免复用脏条目。
	e.dropItems(name)
	if at, ok := s.PendingAt(p); ok {
		it := &entry{point: name, ek: kindPending, at: at}
		e.pendingItem[name] = it
		e.events.push(it)
	}
	if at, ok := s.HeartbeatAt(p); ok {
		it := &entry{point: name, ek: kindHeartbeat, at: at}
		e.heartbeatItem[name] = it
		e.events.push(it)
	}
}

// dropItems 从堆中摘除某测点的全部条目（同步事件直接改变状态后使用）。
func (e *Engine) dropItems(name string) {
	if it := e.pendingItem[name]; it != nil {
		if it.index >= 0 {
			e.events.remove(it)
		}
		e.pendingItem[name] = nil
	}
	if it := e.heartbeatItem[name]; it != nil {
		if it.index >= 0 {
			e.events.remove(it)
		}
		e.heartbeatItem[name] = nil
	}
}

// resync 在一条条目刚被 pop 后重建该测点的全部条目：先清掉被弹出条目的映射，
// 摘除可能仍在堆中的兄弟条目，再按新状态重建。
func (e *Engine) resync(name string) {
	e.refresh(name)
}

// tickLocked 反复弹出时刻不大于 t 的最早事件并处理。调用方持有锁。
// 处理某测点前先主动摘除该测点的另一条条目（其键值即将随状态改变），故每次弹出
// 都必然对应一次发出或一次推迟，弹出数 = 发出数 + 推迟数，与测点总数无关。
func (e *Engine) tickLocked(t int64) ([]Event, TickStats) {
	var out []Event
	var st TickStats
	e.lastPopped, e.lastReports, e.lastThrottles = 0, 0, 0
	for e.events.Len() > 0 {
		top := e.events[0]
		if top.at > t {
			break
		}
		it := e.events.pop()
		e.popped++
		st.Popped++

		// 清除被弹出条目的映射（它已不在堆中），其余由 resync 统一摘除重建。
		if it.ek == kindPending {
			e.pendingItem[it.point] = nil
		} else {
			e.heartbeatItem[it.point] = nil
		}

		s := e.states[it.point]
		prm, _ := e.profiles.Get(it.point)

		var at int64
		var exists, isHeartbeat bool
		if it.ek == kindPending {
			at, exists = s.PendingAt(prm)
		} else {
			at, exists = s.HeartbeatAt(prm)
			isHeartbeat = true
		}
		if !exists || at != it.at {
			// 防御性分支：正常不会进入（兄弟条目已主动摘除）。
			e.refresh(it.point)
			continue
		}

		n := at / e.wn
		if e.used[n] >= e.u {
			s.Throttle((n + 1) * e.wn)
			e.throttled++
			st.Throttled++
			e.resync(it.point)
			continue
		}

		v := s.CurV()
		var why Reason
		if isHeartbeat {
			why = Heartbeat
		} else if !s.HasLast() {
			why = First
		} else if at == s.CurAt() {
			why = Change
		} else {
			why = Trailing
		}
		s.Report(at, v)
		e.used[n]++
		st.Reports++
		out = append(out, Event{Kind: KindReport, Point: it.point, At: at, V: v, Reason: why})
		e.resync(it.point)
	}
	e.lastPopped, e.lastReports, e.lastThrottles = st.Popped, st.Reports, st.Throttled
	return out, st
}

func (e *Engine) SetProfile(point string, t, db, minI, maxI, lo, hi int64) ([]Event, error) {
	prm := profile.Params{DB: db, MinInterval: minI, MaxInterval: maxI, Low: lo, High: hi}
	if t < 0 || t > profile.MaxTime || !validValue(lo) || !validValue(hi) || !profile.ValidParams(prm) {
		return nil, ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.checkTime(t); err != nil {
		return nil, err
	}

	out, _ := e.tickLocked(t)

	if _, existed := e.profiles.Get(point); !existed {
		e.states[point] = filter.NewState()
	}
	e.profiles.Set(point, prm)
	e.refresh(point)

	evs, _ := e.tickLocked(t)
	out = append(out, evs...)
	e.clock = t
	return out, nil
}

func (e *Engine) Sample(point string, t, v int64) ([]Event, error) {
	if t < 0 || t > profile.MaxTime || !validValue(v) {
		return nil, ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 非法参数（含单步超限）先于时钟回退判定，二者先于测点不存在。
	if err := e.checkTime(t); err != nil {
		return nil, err
	}
	s, ok := e.states[point]
	if !ok {
		return nil, ErrNoPoint
	}
	prm, _ := e.profiles.Get(point)

	out, _ := e.tickLocked(t)

	recovered, faulted := s.Ingest(t, v, prm, e.q)
	if faulted {
		// 进入故障：故障前待报作废，故障期间无任何事件。
		e.dropItems(point)
		out = append(out, Event{Kind: KindFault, Point: point, At: t})
	} else if recovered {
		// 恢复样本：Recover 与 Report(Recover) 同步发出，绕过额度。
		e.dropItems(point)
		out = append(out,
			Event{Kind: KindRecover, Point: point, At: t},
			Event{Kind: KindReport, Point: point, At: t, V: v, Reason: Recover},
		)
	}
	// 有效样本（含恢复）改变 cur，需重算事件；普通无效样本不改任何事件状态，
	// 绝不能先删条目（否则心跳条目会凭空丢失）。
	if prm.Valid(v) {
		e.refresh(point)
	}

	evs, _ := e.tickLocked(t)
	out = append(out, evs...)
	e.clock = t
	return out, nil
}

func (e *Engine) Tick(t int64) ([]Event, error) {
	if t < 0 || t > profile.MaxTime {
		return nil, ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.checkTime(t); err != nil {
		return nil, err
	}
	out, _ := e.tickLocked(t)
	e.clock = t
	return out, nil
}

func (e *Engine) Throttled() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.throttled
}

// Popped 返回自引擎建立以来事件结构的累计弹出条目数。
func (e *Engine) Popped() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.popped
}

// LastTickStats 返回最近一次 Tick 阶段的弹出/发出/推迟统计。
func (e *Engine) LastTickStats() TickStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return TickStats{Popped: e.lastPopped, Reports: e.lastReports, Throttled: e.lastThrottles}
}
