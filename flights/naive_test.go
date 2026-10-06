package flights

import (
	"cmp"
	"slices"
)

// naive 朴素对照模型：每次变更后对全部航班从头整表推算。
// 与引擎的增量实现相互独立，用于随机比对验证。
type naive struct {
	cfg     Config
	flights []Flight // 按 (计划起飞, ID) 排序
	order   map[string]int
	delays  map[string]int
	cancels map[string]int
	frozen  map[string]bool
	res     map[string]Result
}

func newNaive(flights []Flight, cfg Config) *naive {
	sorted := slices.Clone(flights)
	slices.SortFunc(sorted, func(a, b Flight) int {
		if c := cmp.Compare(a.SchedDep, b.SchedDep); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	n := &naive{
		cfg:     cfg,
		flights: sorted,
		order:   map[string]int{},
		delays:  map[string]int{},
		cancels: map[string]int{},
		frozen:  map[string]bool{},
		res:     map[string]Result{},
	}
	for i, f := range sorted {
		n.order[f.ID] = i
	}
	n.recompute(0)
	return n
}

// chainState 整表扫描时沿单条链携带的状态。
type chainState struct {
	init        bool
	base        string // 首段起飞机场
	loc         string // 最近未取消段的到达机场
	arr         int
	has         bool // 已有未取消段
	grounded    bool
	groundRoot  int
	dutyStart   int
	dutyBlocked bool
	dutyRoot    int
}

func (n *naive) freezeDeparted(now int) {
	for _, f := range n.flights {
		if n.frozen[f.ID] {
			continue
		}
		if r, ok := n.res[f.ID]; ok && r.Status != StatusCancelled && r.Dep <= now {
			n.frozen[f.ID] = true
		}
	}
}

func (n *naive) recompute(now int) {
	n.freezeDeparted(now)
	am := map[string]*chainState{}
	cm := map[string]*chainState{}
	get := func(m map[string]*chainState, k string) *chainState {
		s := m[k]
		if s == nil {
			s = &chainState{}
			m[k] = s
		}
		return s
	}
	for _, f := range n.flights {
		a := get(am, f.Aircraft)
		c := get(cm, f.Crew)
		if !a.init {
			a.init = true
			a.base = f.Origin
		}
		if !c.init {
			c.init = true
			c.base = f.Origin
		}
		if n.frozen[f.ID] {
			r := n.res[f.ID]
			a.has, a.loc, a.arr, a.grounded = true, f.Dest, r.Arr, false
			if !c.has {
				c.dutyStart = r.Dep
			}
			c.has, c.loc, c.arr, c.grounded = true, f.Dest, r.Arr, false
			continue
		}
		best, bestRoot := ReasonNone, inf
		consider := func(reason Reason, root int) {
			if best == ReasonNone || root < bestRoot || (root == bestRoot && reasonRank(reason) < reasonRank(best)) {
				best, bestRoot = reason, root
			}
		}
		locA := a.base
		if a.has {
			locA = a.loc
		}
		if f.Origin != locA {
			consider(ReasonNoAircraft, a.groundRoot)
		}
		locC := c.base
		if c.has {
			locC = c.loc
		}
		if f.Origin != locC {
			consider(ReasonNoCrew, c.groundRoot)
		}
		if n.cancels[f.ID] > 0 {
			consider(ReasonInjected, n.order[f.ID])
		}
		if c.dutyBlocked {
			consider(ReasonDuty, c.dutyRoot)
		}
		var r Result
		root := bestRoot
		if best != ReasonNone {
			r = Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: best}
		} else {
			dep := f.SchedDep + n.delays[f.ID]
			if a.has && a.arr+n.cfg.MinTurnaround > dep {
				dep = a.arr + n.cfg.MinTurnaround
			}
			if c.has && c.arr+n.cfg.MinConnection > dep {
				dep = c.arr + n.cfg.MinConnection
			}
			if cf, ok := n.cfg.Curfews[f.Origin]; ok && cf.Contains(dep) {
				dep = cf.End
			}
			arr := dep + f.Duration
			dutyStart := c.dutyStart
			if !c.has {
				dutyStart = dep
			}
			switch cf := n.cfg.Curfews[f.Dest]; {
			case cf.Contains(arr):
				r = Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: ReasonCurfew}
				root = n.order[f.ID]
			case arr-dutyStart > n.cfg.MaxDuty:
				r = Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: ReasonDuty}
				root = n.order[f.ID]
				c.dutyBlocked = true
				c.dutyRoot = root
			default:
				st := StatusOnTime
				if dep > f.SchedDep {
					st = StatusDelayed
				}
				r = Result{Status: st, Dep: dep, Arr: arr}
			}
		}
		if r.Status == StatusCancelled {
			if !a.grounded {
				a.grounded = true
				a.groundRoot = root
			}
			if !c.grounded {
				c.grounded = true
				c.groundRoot = root
			}
		} else {
			a.has, a.loc, a.arr, a.grounded = true, f.Dest, r.Arr, false
			if !c.has {
				c.dutyStart = r.Dep
			}
			c.has, c.loc, c.arr, c.grounded = true, f.Dest, r.Arr, false
		}
		n.res[f.ID] = r
	}
	n.freezeDeparted(now)
}

func (n *naive) injectDelay(id string, minutes, now int) {
	n.delays[id] += minutes
	n.recompute(now)
}

func (n *naive) injectCancel(id string, now int) {
	n.cancels[id]++
	n.recompute(now)
}

func (n *naive) withdrawDelay(id string, minutes, now int) {
	n.delays[id] -= minutes
	n.recompute(now)
}

func (n *naive) withdrawCancel(id string, now int) {
	n.cancels[id]--
	n.recompute(now)
}
