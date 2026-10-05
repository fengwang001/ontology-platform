// Package uplink 实现固定窗口上行额度、推迟以及引擎编排
// （Tick/Sample/SetProfile）。事件为状态的纯函数，由每测点至多一条
// 的最小堆维护；所有导出方法持互斥锁，并发调用等价于某个串行顺序。
package uplink

import (
	"container/heap"
	"fmt"
	"sync"

	"ontology/filter"
	"ontology/profile"
)

// OutputKind 为输出种类。
type OutputKind int

const (
	OutReport OutputKind = iota
	OutFault
	OutRecover
)

// Output 为一次上行输出。Report 携带取值与原因；Fault/Recover 仅用 Kind/Point/T。
type Output struct {
	Kind   OutputKind
	Point  string
	T      int64
	V      int64
	Reason profile.Reason
}

func (o Output) String() string {
	switch o.Kind {
	case OutReport:
		return fmt.Sprintf("Report(%s,%d,%d,%s)", o.Point, o.T, o.V, o.Reason)
	case OutFault:
		return fmt.Sprintf("Fault(%s,%d)", o.Point, o.T)
	case OutRecover:
		return fmt.Sprintf("Recover(%s,%d)", o.Point, o.T)
	}
	return "Unknown"
}

// entry 为事件堆条目：某测点当前最早的事件。
type entry struct {
	at    int64
	kind  filter.Kind
	point string
	idx   int
}

// eventHeap 按 (时刻, 测点名字节序, 待报先于心跳) 排序。
type eventHeap []*entry

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.at != b.at {
		return a.at < b.at
	}
	if a.point != b.point {
		return a.point < b.point
	}
	return a.kind < b.kind
}

func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *eventHeap) Push(x any) {
	e := x.(*entry)
	e.idx = len(*h)
	*h = append(*h, e)
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// Engine 为遥测例外上报过滤器。
type Engine struct {
	mu        sync.Mutex
	wn        int64 // 窗口长
	u         int64 // 每窗口额度
	q         int64 // 故障阈值
	clock     int64
	points    map[string]*profile.State
	used      map[int64]int64 // 窗口号 -> 已用额度
	entries   map[string]*entry
	ev        eventHeap
	Throttled int64 // 被推迟的事件总数
	popped    int64 // 最近一次 tick 从事件堆弹出的条目数
}

// New 创建引擎：窗口长 wn∈[1,1e9]，每窗口额度 u∈[1,1e4]，故障阈值 q∈[1,100]。
func New(wn, u, q int64) (*Engine, error) {
	if wn < 1 || wn > 1_000_000_000 || u < 1 || u > 10_000 || q < 1 || q > 100 {
		return nil, profile.ErrInvalid
	}
	return &Engine{
		wn:      wn,
		u:       u,
		q:       q,
		points:  make(map[string]*profile.State),
		used:    make(map[int64]int64),
		entries: make(map[string]*entry),
	}, nil
}

// Popped 返回最近一次 Tick 从事件结构弹出的条目数（用于不变量验证）。
func (e *Engine) Popped() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.popped
}

// checkClock 校验时刻：范围非法 ErrInvalid 优先于时钟回退 ErrClockBack。
func (e *Engine) checkClock(t int64) error {
	if t < 0 || t > profile.MaxT {
		return profile.ErrInvalid
	}
	if t < e.clock {
		return profile.ErrClockBack
	}
	if t-e.clock > profile.MaxStep {
		return profile.ErrInvalid
	}
	return nil
}

// refresh 在测点状态变化后重算其最早事件并维护堆项（每测点至多一条）。
func (e *Engine) refresh(p string) {
	at, k, ok := filter.Next(e.points[p])
	ent, has := e.entries[p]
	switch {
	case ok && has:
		if ent.at != at || ent.kind != k {
			ent.at, ent.kind = at, k
			heap.Fix(&e.ev, ent.idx)
		}
	case ok:
		ent := &entry{at: at, kind: k, point: p}
		e.entries[p] = ent
		heap.Push(&e.ev, ent)
	case has:
		heap.Remove(&e.ev, ent.idx)
		delete(e.entries, p)
	}
}

// tick 反复处理时刻不大于 t 的最早事件，直到没有这样的事件。
func (e *Engine) tick(t int64, out *[]Output) {
	e.popped = 0
	for len(e.ev) > 0 {
		top := e.ev[0]
		if top.at > t {
			return
		}
		heap.Pop(&e.ev)
		delete(e.entries, top.point)
		e.popped++
		st := e.points[top.point]
		n := top.at / e.wn
		if e.used[n] >= e.u {
			st.Defer = (n + 1) * e.wn
			e.Throttled++
		} else {
			e.used[n]++
			*out = append(*out, Output{
				Kind:   OutReport,
				Point:  top.point,
				T:      top.at,
				V:      st.CurV,
				Reason: filter.ReasonFor(st, top.kind, top.at),
			})
			st.LastV, st.HasLast, st.LastAt, st.Defer = st.CurV, true, top.at, 0
		}
		e.refresh(top.point)
	}
}

// Tick 处理时刻不大于 t 的全部事件。
func (e *Engine) Tick(t int64) ([]Output, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(t); err != nil {
		return nil, err
	}
	var out []Output
	e.tick(t, &out)
	e.clock = t
	return out, nil
}

// Sample 登记样本：先 Tick(t)，再登记，再 Tick(t)。
func (e *Engine) Sample(p string, t, v int64) ([]Output, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !profile.ValueOK(v) {
		return nil, profile.ErrInvalid
	}
	if err := e.checkClock(t); err != nil {
		return nil, err
	}
	st, ok := e.points[p]
	if !ok {
		return nil, profile.ErrNoPoint
	}
	var out []Output
	e.tick(t, &out)
	if v >= st.Lo && v <= st.Hi {
		st.Bad = 0
		st.CurTS, st.CurV, st.HasCur = t, v, true
		if st.Fault {
			st.Fault = false
			out = append(out,
				Output{Kind: OutRecover, Point: p, T: t},
				Output{Kind: OutReport, Point: p, T: t, V: v, Reason: profile.ReasonRecover})
			st.LastV, st.HasLast, st.LastAt, st.Defer = v, true, t, 0
		}
	} else {
		st.Bad++
		if st.Bad == e.q && !st.Fault {
			st.Fault = true
			out = append(out, Output{Kind: OutFault, Point: p, T: t})
		}
	}
	e.refresh(p)
	e.tick(t, &out)
	e.clock = t
	return out, nil
}

// SetProfile 建立或热更新测点：先 Tick(t)，再替换参数（保留运行态），再 Tick(t)。
func (e *Engine) SetProfile(p string, t, db, minI, maxI, lo, hi int64) ([]Output, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prm := profile.Params{DB: db, MinI: minI, MaxI: maxI, Lo: lo, Hi: hi}
	if !prm.Valid() {
		return nil, profile.ErrInvalid
	}
	if err := e.checkClock(t); err != nil {
		return nil, err
	}
	var out []Output
	e.tick(t, &out)
	st, ok := e.points[p]
	if !ok {
		st = &profile.State{}
		e.points[p] = st
	}
	st.Params = prm
	e.refresh(p)
	e.tick(t, &out)
	e.clock = t
	return out, nil
}
