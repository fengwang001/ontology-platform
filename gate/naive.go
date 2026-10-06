// naive.go：与正式实现完全独立的朴素模型。
// 每个登机口用一个线性切片保存当前全部占用；所有判定均全量扫描。
// 规则实现刻意不依赖 tree/system 的内部结构（仅共享 model.go 的纯类型与
// 优先级函数），供随机序列对拍，防止“同一份代码两边错”。
package gatealloc

import "sync"

type naiveGate struct {
	spec GateSpec
	adj  map[string]bool
	occ  []Occupancy
}

type naiveFlight struct {
	spec         FlightSpec
	arr, dep     int
	deplaneGate  string
	boardingGate string
	split        bool
}

// Naive 是朴素对照系统，公开与 System 相同的操作。
type Naive struct {
	mu      sync.Mutex
	cfg     Config
	gates   map[string]*naiveGate
	flights map[string]*naiveFlight
	now     int
}

// NewNaive 按相同输入构造朴素模型（仅复用输入校验，不共享运行状态）。
func NewNaive(cfg Config, gates []GateSpec, flights []FlightSpec, adjs []Adj) (*Naive, error) {
	if _, err := New(cfg, gates, flights, adjs); err != nil {
		return nil, err
	}
	n := &Naive{cfg: cfg, gates: map[string]*naiveGate{}, flights: map[string]*naiveFlight{}}
	for _, gs := range gates {
		n.gates[gs.ID] = &naiveGate{spec: gs, adj: map[string]bool{}}
	}
	for _, fs := range flights {
		n.flights[fs.ID] = &naiveFlight{
			spec:  fs,
			arr:   fs.SchedArr,
			dep:   fs.SchedDep,
			split: cfg.wholeLen(fs.SchedArr, fs.SchedDep) > cfg.MaxStay,
		}
	}
	for _, a := range adjs {
		n.gates[a.A].adj[a.B] = true
		n.gates[a.B].adj[a.A] = true
	}
	return n, nil
}

func (n *Naive) gateOf(f *naiveFlight, seg Segment) string {
	if seg == SegBoarding {
		return f.boardingGate
	}
	return f.deplaneGate
}

func (n *Naive) setGate(f *naiveFlight, seg Segment, g string) {
	if seg == SegBoarding {
		f.boardingGate = g
	} else {
		f.deplaneGate = g
	}
}

func (n *Naive) remove(gateID, flightID string, seg Segment) {
	g := n.gates[gateID]
	out := g.occ[:0]
	for _, o := range g.occ {
		if o.Flight == flightID && o.Seg == seg {
			continue
		}
		out = append(out, o)
	}
	g.occ = out
}

func (n *Naive) interval(f *naiveFlight, seg Segment) (int, int, bool) {
	switch {
	case seg == SegWhole && !f.split:
		return f.arr, f.dep + n.cfg.Buffer, true
	case seg == SegDeplane && f.split:
		return f.arr, f.arr + n.cfg.DeplaneDur, true
	case seg == SegBoarding && f.split:
		return f.dep - n.cfg.BoardingDur, f.dep + n.cfg.Buffer, true
	default:
		return 0, 0, false
	}
}

// earliestConflict 线性找出起点最早、标识最小的同口冲突。
func (n *Naive) earliestConflict(g *naiveGate, self string, start, end int) string {
	bestFlight := ""
	bestStart := 0
	for _, o := range g.occ {
		if o.Flight == self || !overlap(start, end, o.Start, o.End) {
			continue
		}
		if bestFlight == "" || o.Start < bestStart || (o.Start == bestStart && o.Flight < bestFlight) {
			bestFlight, bestStart = o.Flight, o.Start
		}
	}
	return bestFlight
}

func (n *Naive) adjacencyViolation(g *naiveGate, self string, selfClass Class, start, end int) bool {
	if selfClass != Class3 {
		return false
	}
	for nID := range g.adj {
		for _, o := range n.gates[nID].occ {
			if o.Flight != self && n.flights[o.Flight].spec.Class == Class3 && overlap(start, end, o.Start, o.End) {
				return true
			}
		}
	}
	return false
}

func (n *Naive) segList(f *naiveFlight) []Segment {
	if f.split {
		return []Segment{SegDeplane, SegBoarding}
	}
	return []Segment{SegWhole}
}

// Assign 朴素指派。
func (n *Naive) Assign(now int, flightID, gateID string, seg Segment) AssignResult {
	n.mu.Lock()
	defer n.mu.Unlock()

	if flightID == "" || gateID == "" || seg < SegWhole || seg > SegBoarding || now < 0 {
		return assignFail(RejInvalidParam, "assign: bad arguments", "")
	}
	if now < n.now {
		return assignFail(RejClockBack, "assign: clock moved backwards", "")
	}
	f := n.flights[flightID]
	g := n.gates[gateID]
	if f == nil || g == nil {
		return assignFail(RejNotFound, "assign: unknown flight or gate", "")
	}
	validSeg := (!f.split && seg == SegWhole) || (f.split && seg != SegWhole)
	if !validSeg {
		return assignFail(RejInvalidParam, "assign: segment not applicable", "")
	}
	tmp := &flightState{arr: f.arr, dep: f.dep, spec: f.spec}
	if lockedForAssign(now, tmp, seg) {
		return assignFail(RejArrivedLocked, "assign: locked", "")
	}
	if !classOK(f.spec.Class, g.spec.MaxClass) {
		return assignFail(RejClassIncompat, "assign: class", "")
	}
	if !kindOK(f.spec.Kind, g.spec.Kind) {
		return assignFail(RejKindMismatch, "assign: kind", "")
	}
	start, end, _ := n.interval(f, seg)

	oldGate := n.gateOf(f, seg)
	var saved Occupancy
	hadOld := false
	if oldGate != "" {
		for _, o := range n.gates[oldGate].occ {
			if o.Flight == flightID && o.Seg == seg {
				saved, hadOld = o, true
			}
		}
		n.remove(oldGate, flightID, seg)
	}
	restore := func() {
		if hadOld {
			n.gates[oldGate].occ = append(n.gates[oldGate].occ, saved)
		}
	}

	if n.adjacencyViolation(g, flightID, f.spec.Class, start, end) {
		restore()
		return assignFail(RejAdjacency, "assign: adjacency", "")
	}
	if cf := n.earliestConflict(g, flightID, start, end); cf != "" {
		restore()
		return assignFail(RejTimeConflict, "assign: conflict", cf)
	}

	g.occ = append(g.occ, Occupancy{Flight: flightID, Seg: seg, Gate: gateID, Start: start, End: end})
	n.setGate(f, seg, gateID)
	n.now = now
	return AssignResult{OK: true}
}

type naiveClash struct {
	flight string
	seg    Segment
	gate   string
	start  int
	cause  Reason
}

// Delay 朴素延误更新与挤占裁决。
func (n *Naive) Delay(now int, flightID string, mask DelayMask, newArr, newDep int) DelayResult {
	n.mu.Lock()
	defer n.mu.Unlock()

	if flightID == "" || mask == 0 || mask&^(MaskArr|MaskDep) != 0 || newArr < 0 || newDep < 0 || now < 0 {
		return delayFail(RejInvalidParam, "delay: bad arguments")
	}
	if now < n.now {
		return delayFail(RejClockBack, "delay: clock moved backwards")
	}
	f := n.flights[flightID]
	if f == nil {
		return delayFail(RejNotFound, "delay: unknown flight")
	}
	na, nd := f.arr, f.dep
	if mask&MaskArr != 0 {
		if newArr < f.arr {
			return delayFail(RejInvalidParam, "delay: arrival early")
		}
		na = newArr
	}
	if mask&MaskDep != 0 {
		if newDep < f.dep {
			return delayFail(RejInvalidParam, "delay: departure early")
		}
		nd = newDep
	}
	if na > nd {
		return delayFail(RejInvalidParam, "delay: arr after dep")
	}
	if mask&MaskArr != 0 && now >= f.arr {
		return delayFail(RejArrivedLocked, "delay: arrived")
	}
	if mask&MaskDep != 0 && now >= f.dep-f.spec.BoardLead {
		return delayFail(RejArrivedLocked, "delay: boarding")
	}

	// 回滚需要恢复所有可能被挤占者的登机口，其集合无法事先精确枚举，
	// 故全量备份每个登机口切片（朴素模型本就以可读性优先，不求性能）。
	gateBak := map[string][]Occupancy{}
	for id, g := range n.gates {
		cp := make([]Occupancy, len(g.occ))
		copy(cp, g.occ)
		gateBak[id] = cp
	}
	flightBak := *f
	allFlightBak := map[string]naiveFlight{}
	for id, fl := range n.flights {
		allFlightBak[id] = *fl
	}
	rollback := func() {
		*f = flightBak
		for id, fl := range allFlightBak {
			*n.flights[id] = fl
		}
		for id, occ := range gateBak {
			cp := make([]Occupancy, len(occ))
			copy(cp, occ)
			n.gates[id].occ = cp
		}
	}

	wasSplit := f.split
	for _, seg := range n.segList(f) {
		if g := n.gateOf(f, seg); g != "" {
			n.remove(g, flightID, seg)
		}
	}
	f.arr, f.dep = na, nd
	f.split = n.cfg.wholeLen(na, nd) > n.cfg.MaxStay
	becameSplit := !wasSplit && f.split
	if wasSplit != f.split {
		f.deplaneGate = flightBak.deplaneGate
		f.boardingGate = ""
	}

	var evictions []Eviction
	var failReason Reason
loopSegs:
	for _, seg := range n.segList(f) {
		gateID := n.gateOf(f, seg)
		if gateID == "" {
			continue
		}
		g := n.gates[gateID]
		start, end, _ := n.interval(f, seg)

		collect := func() []naiveClash {
			var cs []naiveClash
			for _, o := range g.occ {
				if o.Flight != flightID && overlap(start, end, o.Start, o.End) {
					cs = append(cs, naiveClash{o.Flight, o.Seg, gateID, o.Start, RejTimeConflict})
				}
			}
			if f.spec.Class == Class3 {
				for nID := range g.adj {
					for _, o := range n.gates[nID].occ {
						if o.Flight != flightID && n.flights[o.Flight].spec.Class == Class3 &&
							overlap(start, end, o.Start, o.End) {
							cs = append(cs, naiveClash{o.Flight, o.Seg, nID, o.Start, RejAdjacency})
						}
					}
				}
			}
			return cs
		}

		for {
			cs := collect()
			if len(cs) == 0 {
				break
			}
			best := 0
			for i := 1; i < len(cs); i++ {
				a, b := cs[best], cs[i]
				if b.start < a.start ||
					(b.start == a.start && b.flight < a.flight) ||
					(b.start == a.start && b.flight == a.flight && b.seg < a.seg) ||
					(b.start == a.start && b.flight == a.flight && b.seg == a.seg && b.gate < a.gate) {
					best = i
				}
			}
			c := cs[best]
			other := n.flights[c.flight]
			moverWins := higherPriority(f.spec.Kind, f.spec.Class, f.spec.SchedArr, flightID,
				other.spec.Kind, other.spec.Class, other.spec.SchedArr, c.flight)
			if moverWins {
				tmp := &flightState{arr: other.arr, dep: other.dep, spec: other.spec}
				if protected(now, tmp, c.seg) {
					failReason = RejCannotEvict
					break loopSegs
				}
				n.remove(c.gate, c.flight, c.seg)
				n.setGate(other, c.seg, "")
				evictions = append(evictions, Eviction{c.flight, c.seg, c.gate, flightID, c.cause})
				continue
			}
			tmp := &flightState{arr: f.arr, dep: f.dep, spec: f.spec}
			if protected(now, tmp, seg) {
				failReason = RejCannotEvict
				break loopSegs
			}
			n.remove(gateID, flightID, seg)
			n.setGate(f, seg, "")
			evictions = append(evictions, Eviction{flightID, seg, gateID, c.flight, c.cause})
			break
		}
	}

	if failReason != 0 {
		rollback()
		return delayFail(failReason, "delay: cannot evict")
	}

	for _, seg := range n.segList(f) {
		gateID := n.gateOf(f, seg)
		if gateID == "" {
			continue
		}
		start, end, _ := n.interval(f, seg)
		n.gates[gateID].occ = append(n.gates[gateID].occ, Occupancy{
			Flight: flightID, Seg: seg, Gate: gateID, Start: start, End: end,
		})
	}
	n.now = now
	return DelayResult{OK: true, BecameSplit: becameSplit, Evicted: canonicalEvictions(evictions)}
}

// canonicalEvictions 把挤占记录按 (Victim, Seg) 规范化，保证两套实现比对一致。
func canonicalEvictions(es []Eviction) []Eviction {
	out := append([]Eviction(nil), es...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if b.Victim < a.Victim || (b.Victim == a.Victim && b.Seg < a.Seg) {
				out[j-1], out[j] = b, a
			}
		}
	}
	return out
}

// OccupantAt 朴素时刻占用者查询。
func (n *Naive) OccupantAt(gateID string, t int) (string, Segment, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	g, ok := n.gates[gateID]
	if !ok {
		return "", SegWhole, false
	}
	best := ""
	for _, o := range g.occ {
		if o.Start <= t && t < o.End {
			if best == "" || o.Flight < best {
				best = o.Flight
			}
		}
	}
	if best == "" {
		return "", SegWhole, false
	}
	for _, o := range g.occ {
		if o.Flight == best && o.Start <= t && t < o.End {
			return o.Flight, o.Seg, true
		}
	}
	return "", SegWhole, false
}

// Now 返回最近接受操作的时刻。
func (n *Naive) Now() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.now
}

// Snapshot 返回完整确定性快照。
func (n *Naive) Snapshot() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	snap := Snapshot{Now: n.now, Flights: map[string]FlightState{}}
	for id, f := range n.flights {
		snap.Flights[id] = FlightState{
			Arr: f.arr, Dep: f.dep, Split: f.split,
			DeplaneGate: f.deplaneGate, BoardingGate: f.boardingGate,
		}
	}
	for _, g := range n.gates {
		for _, o := range g.occ {
			snap.Assignments = append(snap.Assignments, Assignment{
				Flight: o.Flight, Seg: o.Seg, Gate: g.spec.ID, Start: o.Start, End: o.End,
			})
		}
	}
	return sortSnapshot(snap)
}

func sortSnapshot(snap Snapshot) Snapshot {
	for i := 1; i < len(snap.Assignments); i++ {
		for j := i; j > 0; j-- {
			a, b := snap.Assignments[j-1], snap.Assignments[j]
			if b.Flight < a.Flight || (b.Flight == a.Flight && b.Seg < a.Seg) {
				snap.Assignments[j-1], snap.Assignments[j] = b, a
			}
		}
	}
	return snap
}
