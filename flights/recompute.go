package flights

import "container/heap"

// legState 一段航班的推算结论及传导所需的派生量。
type legState struct {
	res         Result
	root        int  // 取消根航班的全局拓扑序（仅取消时有效）
	dutyStart   int  // 机组第一个未取消段的实际起飞（仅未取消时有效）
	dutyBlocked bool // 机组链上此前已发生值勤超限（其后所有段均取消）
	dutyRoot    int  // 值勤超限根航班的全局拓扑序
}

const inf = int(^uint(0) >> 1)

// compute 依据当前注入记录与上游航段的最终结论，重算一段航班。
// 所有依赖（飞机链 / 机组链上的前段）在全局拓扑序上都先于本段，
// 因此按拓扑序各段只需求值一次即得唯一不动点。
func (e *Engine) compute(l *leg) legState {
	best := ReasonNone
	bestRoot := inf
	consider := func(r Reason, root int) {
		if best == ReasonNone || root < bestRoot || (root == bestRoot && reasonRank(r) < reasonRank(best)) {
			best, bestRoot = r, root
		}
	}

	// 飞机链：最近未取消前段决定飞机当前位置与过站约束；
	// 取消根取最后未取消段之后第一段被取消航班的根（传导链上最早发生者）。
	ach := e.aChains[l.Aircraft]
	var ap *leg
	groundRootA := 0
	for i := l.aIdx - 1; i >= 0; i-- {
		p := ach[i]
		if p.st.res.Status != StatusCancelled {
			ap = p
			break
		}
		groundRootA = p.st.root
	}
	locA := ach[0].Origin
	if ap != nil {
		locA = ap.Dest
	}
	if l.Origin != locA {
		consider(ReasonNoAircraft, groundRootA)
	}

	// 机组链同理。
	cch := e.cChains[l.Crew]
	var cp *leg
	groundRootC := 0
	for i := l.cIdx - 1; i >= 0; i-- {
		p := cch[i]
		if p.st.res.Status != StatusCancelled {
			cp = p
			break
		}
		groundRootC = p.st.root
	}
	locC := cch[0].Origin
	if cp != nil {
		locC = cp.Dest
	}
	if l.Origin != locC {
		consider(ReasonNoCrew, groundRootC)
	}

	if l.cancels > 0 {
		consider(ReasonInjected, l.order)
	}

	// 值勤超限一旦在机组链上发生，其后所有段均取消（根沿链继承）。
	dutyBlocked := false
	dutyRoot := 0
	if l.cIdx > 0 {
		if pred := cch[l.cIdx-1]; pred.st.dutyBlocked {
			dutyBlocked = true
			dutyRoot = pred.st.dutyRoot
			consider(ReasonDuty, dutyRoot)
		}
	}

	if best != ReasonNone {
		return legState{
			res:         Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: best},
			root:        bestRoot,
			dutyBlocked: dutyBlocked,
			dutyRoot:    dutyRoot,
		}
	}

	dep := l.SchedDep + l.delaySum
	if ap != nil && ap.st.res.Arr+e.cfg.MinTurnaround > dep {
		dep = ap.st.res.Arr + e.cfg.MinTurnaround
	}
	if cp != nil && cp.st.res.Arr+e.cfg.MinConnection > dep {
		dep = cp.st.res.Arr + e.cfg.MinConnection
	}
	if c, ok := e.cfg.Curfews[l.Origin]; ok && c.Contains(dep) {
		dep = c.End
	}
	arr := dep + l.Duration
	if c, ok := e.cfg.Curfews[l.Dest]; ok && c.Contains(arr) {
		return legState{
			res:         Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: ReasonCurfew},
			root:        l.order,
			dutyBlocked: dutyBlocked,
			dutyRoot:    dutyRoot,
		}
	}
	dutyStart := dep
	if cp != nil {
		dutyStart = cp.st.dutyStart
	}
	if arr-dutyStart > e.cfg.MaxDuty {
		return legState{
			res:         Result{Status: StatusCancelled, Dep: -1, Arr: -1, Reason: ReasonDuty},
			root:        l.order,
			dutyBlocked: true,
			dutyRoot:    l.order,
		}
	}
	st := StatusOnTime
	if dep > l.SchedDep {
		st = StatusDelayed
	}
	return legState{
		res:         Result{Status: st, Dep: dep, Arr: arr},
		dutyStart:   dutyStart,
		dutyBlocked: dutyBlocked,
		dutyRoot:    dutyRoot,
	}
}

// propagate 从被变更的航段出发做增量重算：按全局拓扑序弹出工作项，
// 仅当某段结论实际变化时才沿飞机链与机组链向后扩散。
// 每段通过“最近未取消前段”观察上游，因此状态变化的影响范围是
// 后继的连续取消游程及其后第一段未取消航班（它们的最近未取消前段
// 或取消根可能因此改变），扩散到未取消段为止。
// 依赖边全部指向拓扑序更大的航段，每段在一次变更中至多被重算一次，
// 开销正比于受影响区域大小，与无关航段数量无关。
func (e *Engine) propagate(seed *leg) {
	h := &legHeap{}
	seen := make(map[*leg]bool)
	push := func(l *leg) {
		if l == nil || seen[l] {
			return
		}
		seen[l] = true
		heap.Push(h, l)
	}
	push(seed)
	pushRun := func(ch []*leg, idx int) {
		for i := idx + 1; i < len(ch); i++ {
			push(ch[i])
			if ch[i].st.res.Status != StatusCancelled {
				break
			}
		}
	}
	cost := 0
	for h.Len() > 0 {
		l := heap.Pop(h).(*leg)
		if l.frozen {
			continue
		}
		cost++
		ns := e.compute(l)
		if ns != l.st {
			l.st = ns
			if ns.res.Status != StatusCancelled {
				e.pushFreeze(l)
			}
			pushRun(e.aChains[l.Aircraft], l.aIdx)
			pushRun(e.cChains[l.Crew], l.cIdx)
		}
	}
	e.lastCost = cost
}

// legHeap 按全局拓扑序的最小堆。
type legHeap []*leg

func (h legHeap) Len() int           { return len(h) }
func (h legHeap) Less(i, j int) bool { return h[i].order < h[j].order }
func (h legHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *legHeap) Push(x any)        { *h = append(*h, x.(*leg)) }
func (h *legHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// freezeEntry 冻结堆条目：按当前实际起飞时刻排序，惰性删除过期条目。
type freezeEntry struct {
	dep int
	l   *leg
}

type freezeHeap []freezeEntry

func (h freezeHeap) Len() int           { return len(h) }
func (h freezeHeap) Less(i, j int) bool { return h[i].dep < h[j].dep }
func (h freezeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *freezeHeap) Push(x any)        { *h = append(*h, x.(freezeEntry)) }
func (h *freezeHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
