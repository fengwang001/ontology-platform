package delay

import "sort"

// naiveRoot 与引擎内核的 croot 语义一致，但在此独立实现，
// 用 (time, rank, birth) 三元组比较传导链上最早的根。
type naiveRoot struct {
	time   int
	rank   int
	reason Reason
	birth  int
}

func (r *naiveRoot) earlier(o *naiveRoot) bool {
	if r.time != o.time {
		return r.time < o.time
	}
	if r.rank != o.rank {
		return r.rank < o.rank
	}
	return r.birth < o.birth
}

type naiveAc struct {
	airport string
	root    *naiveRoot
	ready   int
	readyS  bool
}

type naiveCrew struct {
	airport string
	root    *naiveRoot
	dead    *naiveRoot
	ready   int
	readyS  bool
	first   int
	firstS  bool
}

func crewFirst(c *naiveCrew, dep int) int {
	if !c.firstS {
		return dep
	}
	return c.first
}

// NaiveSweep 是与引擎增量内核相互独立编写的朴素对照模型：
// 对给定注入集合从空状态出发、按（计划时刻, ID）顺序整表一次性推算。
// frozen 给出当前时刻之前已发生航班的固定结论（执行段或取消段）。
func NaiveSweep(flights []Flight, cfg Config, inj map[string]map[string]Injection,
	frozen map[string]Result) (map[string]Result, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	fs := append([]Flight(nil), flights...)
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Scheduled != fs[j].Scheduled {
			return fs[i].Scheduled < fs[j].Scheduled
		}
		return fs[i].ID < fs[j].ID
	})
	acs := make(map[string]*naiveAc)
	crs := make(map[string]*naiveCrew)
	out := make(map[string]Result)

	for idx, f := range fs {
		a := acs[f.AircraftID]
		if a == nil {
			a = &naiveAc{airport: f.Origin}
			acs[f.AircraftID] = a
		}
		cw := crs[f.CrewID]
		if cw == nil {
			cw = &naiveCrew{airport: f.Origin}
			crs[f.CrewID] = cw
		}

		if fr, ok := frozen[f.ID]; ok {
			if fr.Status == StatusCanceled {
				// 取消段不固定：由注入与冻结的执行段上游重新推出。
				// 直接落到下方统一规则处理。
			} else {
				out[f.ID] = fr
				a.airport, a.root = f.Dest, nil
				a.readyS, a.ready = true, fr.ActualArr+cfg.MinTurnaround
				cw.airport, cw.root = f.Dest, nil
				cw.readyS, cw.ready = true, fr.ActualArr+cfg.MinConnection
				if !cw.firstS {
					cw.first, cw.firstS = fr.ActualDep, true
				}
			}
			continue
		}

		delaySum := 0
		injected := false
		for _, in := range inj[f.ID] {
			if in.Cancel {
				injected = true
			} else {
				delaySum += in.Delay
			}
		}

		doCancel := func(root *naiveRoot, kind Reason) {
			aAbsent := a.root != nil && a.airport != f.Origin
			cAbsent := cw.root != nil && cw.airport != f.Origin
			if !aAbsent {
				a.airport, a.root = f.Origin, root
				switch kind {
				case ReasonInjected, ReasonCurfew:
				default:
					a.root = nil
				}
			}
			if !cAbsent {
				cw.airport, cw.root = f.Origin, root
				switch kind {
				case ReasonInjected, ReasonCurfew, ReasonDutyExceeded, ReasonNoCrew:
				default:
					cw.root = nil
				}
			}
			if root.reason == ReasonDutyExceeded {
				cw.dead = root
			}
			out[f.ID] = Result{Status: StatusCanceled, Reason: kind}
		}

		if injected {
			doCancel(&naiveRoot{time: -1, rank: reasonRank[ReasonInjected],
				reason: ReasonInjected, birth: idx}, ReasonInjected)
			continue
		}

		if a.root != nil && a.airport == f.Origin {
			a.root = nil
		}
		if cw.dead == nil && cw.root != nil && cw.airport == f.Origin {
			cw.root = nil
		}

		var root *naiveRoot
		kind := ReasonNone
		pick := func(r *naiveRoot, k Reason) {
			if r == nil {
				return
			}
			earlierPick := root == nil || r.earlier(root)
			equiv := root != nil && !r.earlier(root) && !root.earlier(r)
			if earlierPick || (equiv && reasonRank[k] < reasonRank[kind]) {
				root, kind = r, k
			}
		}
		if a.root != nil && a.airport != f.Origin {
			pick(a.root, ReasonNoAircraft)
		}
		if cw.dead != nil {
			pick(cw.dead, ReasonDutyExceeded)
		} else if cw.root != nil && cw.airport != f.Origin {
			pick(cw.root, ReasonNoCrew)
		}
		if root != nil {
			doCancel(root, kind)
			continue
		}

		dep := f.Scheduled + delaySum
		if a.readyS && a.ready > dep {
			dep = a.ready
		}
		if cw.readyS && cw.ready > dep {
			dep = cw.ready
		}
		if cf := cfg.Curfews[f.Origin]; cf.Contains(dep) {
			dep = cf.End
		}
		arr := dep + f.Duration

		if cf := cfg.Curfews[f.Dest]; cf.Contains(arr) {
			doCancel(&naiveRoot{time: arr, rank: reasonRank[ReasonCurfew],
				reason: ReasonCurfew, birth: idx}, ReasonCurfew)
			continue
		}
		first := cw.first
		if !cw.firstS {
			first = dep
		}
		if arr-first > cfg.DutyLimit {
			r := cw.dead
			if r == nil {
				r = &naiveRoot{time: arr, rank: reasonRank[ReasonDutyExceeded],
					reason: ReasonDutyExceeded, birth: idx}
			}
			doCancel(r, ReasonDutyExceeded)
			continue
		}

		if !cw.firstS {
			cw.first, cw.firstS = dep, true
		}
		a.airport, a.root = f.Dest, nil
		a.readyS, a.ready = true, arr+cfg.MinTurnaround
		cw.airport, cw.root = f.Dest, nil
		cw.readyS, cw.ready = true, arr+cfg.MinConnection
		st := StatusScheduled
		if dep != f.Scheduled {
			st = StatusDelayed
		}
		out[f.ID] = Result{Status: st, ActualDep: dep, ActualArr: arr}
	}
	return out, nil
}
