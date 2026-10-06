package flights

import (
	"container/heap"
	"sync"
)

// InjectionID 注入记录句柄，撤回时使用。
type InjectionID int64

type injKind int

const (
	injDelay injKind = iota
	injCancel
)

type injection struct {
	flight  *leg
	kind    injKind
	minutes int
	active  bool
}

// Engine 航班延误连锁调整引擎。所有公开方法可并发调用，效果等价于某个串行顺序。
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	legs     []*leg // 全局拓扑序
	byID     map[string]*leg
	aChains  map[string][]*leg
	cChains  map[string][]*leg
	clock    int
	seq      InjectionID
	inj      map[InjectionID]*injection
	fz       freezeHeap
	lastCost int // 上一次被接受变更的重算航段数（用于性能验证）
}

// NewEngine 校验航班表与配置，构建引擎并完成初始推算。
func NewEngine(flights []Flight, cfg Config) (*Engine, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	legs, byID, aChains, cChains, err := buildLegs(flights)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:     cfg,
		legs:    legs,
		byID:    byID,
		aChains: aChains,
		cChains: cChains,
		inj:     map[InjectionID]*injection{},
	}
	for _, l := range e.legs {
		l.st = e.compute(l)
		if l.st.res.Status != StatusCancelled {
			e.pushFreeze(l)
		}
	}
	e.freeze(0)
	return e, nil
}

// departed 航班是否已实际起飞（实际起飞时刻不大于 now）。
func departed(l *leg, now int) bool {
	return l.st.res.Status != StatusCancelled && l.st.res.Dep <= now
}

// InjectDelay 对航班注入延误（分钟），可多次叠加。
func (e *Engine) InjectDelay(flightID string, minutes, now int) (InjectionID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if minutes < 0 || now < 0 {
		return 0, ErrInvalidParam
	}
	if now < e.clock {
		return 0, ErrClockRollback
	}
	l, ok := e.byID[flightID]
	if !ok {
		return 0, ErrFlightNotFound
	}
	if departed(l, now) {
		return 0, ErrAlreadyDeparted
	}
	e.clock = now
	e.freeze(now) // 本时刻前已实际起飞的航班先行冻结
	e.seq++
	e.inj[e.seq] = &injection{flight: l, kind: injDelay, minutes: minutes, active: true}
	l.delaySum += minutes
	e.propagate(l)
	e.freeze(now)
	return e.seq, nil
}

// InjectCancel 对航班注入取消。
func (e *Engine) InjectCancel(flightID string, now int) (InjectionID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < 0 {
		return 0, ErrInvalidParam
	}
	if now < e.clock {
		return 0, ErrClockRollback
	}
	l, ok := e.byID[flightID]
	if !ok {
		return 0, ErrFlightNotFound
	}
	if departed(l, now) {
		return 0, ErrAlreadyDeparted
	}
	e.clock = now
	e.freeze(now) // 本时刻前已实际起飞的航班先行冻结
	e.seq++
	e.inj[e.seq] = &injection{flight: l, kind: injCancel, active: true}
	l.cancels++
	e.propagate(l)
	e.freeze(now)
	return e.seq, nil
}

// Withdraw 撤回一条注入记录。
func (e *Engine) Withdraw(flightID string, id InjectionID, now int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < 0 {
		return ErrInvalidParam
	}
	if now < e.clock {
		return ErrClockRollback
	}
	l, ok := e.byID[flightID]
	if !ok {
		return ErrFlightNotFound
	}
	if departed(l, now) {
		return ErrAlreadyDeparted
	}
	rec, ok := e.inj[id]
	if !ok || !rec.active || rec.flight != l {
		return ErrInjectionNotFound
	}
	e.clock = now
	e.freeze(now) // 本时刻前已实际起飞的航班先行冻结
	rec.active = false
	if rec.kind == injDelay {
		l.delaySum -= rec.minutes
	} else {
		l.cancels--
	}
	e.propagate(l)
	e.freeze(now)
	return nil
}

// Query 查询航班结论，开销与航班总数无关。
func (e *Engine) Query(flightID string) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.byID[flightID]
	if !ok {
		return Result{}, ErrFlightNotFound
	}
	return l.st.res, nil
}

// LastRecomputeCost 返回上一次被接受变更重算的航段数，用于验证增量重算的局部性。
func (e *Engine) LastRecomputeCost() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastCost
}

// pushFreeze 将未取消航段按实际起飞时刻压入冻结堆（惰性删除）。
func (e *Engine) pushFreeze(l *leg) {
	heap.Push(&e.fz, freezeEntry{dep: l.st.res.Dep, l: l})
}

// freeze 把实际起飞时刻不大于 now 的航段冻结，其实际时刻此后不再变化。
func (e *Engine) freeze(now int) {
	for len(e.fz) > 0 && e.fz[0].dep <= now {
		entry := heap.Pop(&e.fz).(freezeEntry)
		l := entry.l
		if !l.frozen && l.st.res.Status != StatusCancelled && l.st.res.Dep == entry.dep {
			l.frozen = true
		}
	}
}
