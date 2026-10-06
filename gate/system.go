package gatealloc

// system.go：正式实现。整个 System 用一把互斥锁串行化所有变更，
// 因此并发调用的结果等价于某个确定的串行顺序；同序列重放得到同结果。

import (
	"sort"
	"sync"
)

type gateState struct {
	spec GateSpec
	adj  map[string]bool
	tree *occTree
}

type flightState struct {
	spec FlightSpec
	arr  int
	dep  int
	// split=false 时整段指派存于 deplaneGate；
	// split=true 时分别为卸客段与登机段登机口，空串表示待分配。
	deplaneGate  string
	boardingGate string
	split        bool
}

// System 是登机口分配系统。
type System struct {
	mu      sync.Mutex
	cfg     Config
	gates   map[string]*gateState
	flights map[string]*flightState
	now     int
}

// New 构建系统并校验配置与静态数据。
func New(cfg Config, gates []GateSpec, flights []FlightSpec, adjs []Adj) (*System, error) {
	if cfg.Buffer < 0 || cfg.MaxStay < 0 || cfg.DeplaneDur < 0 || cfg.BoardingDur < 0 {
		return nil, &OpError{RejInvalidParam, "negative config value", ""}
	}
	s := &System{
		cfg:     cfg,
		gates:   map[string]*gateState{},
		flights: map[string]*flightState{},
	}
	for _, g := range gates {
		if g.ID == "" || g.MaxClass < Class1 || g.MaxClass > Class3 || g.Kind < GateIntl || g.Kind > GateDual {
			return nil, &OpError{RejInvalidParam, "bad gate spec: " + g.ID, ""}
		}
		if _, dup := s.gates[g.ID]; dup {
			return nil, &OpError{RejInvalidParam, "duplicate gate: " + g.ID, ""}
		}
		s.gates[g.ID] = &gateState{spec: g, adj: map[string]bool{}, tree: &occTree{}}
	}
	for _, f := range flights {
		if f.ID == "" || f.Class < Class1 || f.Class > Class3 || f.Kind < KindIntl || f.Kind > KindDomestic ||
			f.SchedArr < 0 || f.SchedDep < f.SchedArr || f.BoardLead < 0 {
			return nil, &OpError{RejInvalidParam, "bad flight spec: " + f.ID, ""}
		}
		if _, dup := s.flights[f.ID]; dup {
			return nil, &OpError{RejInvalidParam, "duplicate flight: " + f.ID, ""}
		}
		s.flights[f.ID] = &flightState{
			spec:  f,
			arr:   f.SchedArr,
			dep:   f.SchedDep,
			split: cfg.wholeLen(f.SchedArr, f.SchedDep) > cfg.MaxStay,
		}
	}
	for _, a := range adjs {
		if a.A == a.B {
			return nil, &OpError{RejInvalidParam, "self adjacency: " + a.A, ""}
		}
		ga, oa := s.gates[a.A]
		gb, ob := s.gates[a.B]
		if !oa || !ob {
			return nil, &OpError{RejInvalidParam, "adjacency to unknown gate", ""}
		}
		ga.adj[a.B] = true
		gb.adj[a.A] = true
	}
	return s, nil
}

// wholeLen 为整段占用长度（含缓冲）。
func (c Config) wholeLen(arr, dep int) int { return dep + c.Buffer - arr }

// segInterval 返回航班某段的占用区间。
func segInterval(cfg Config, f *flightState, seg Segment) (start, end int, ok bool) {
	switch {
	case seg == SegWhole && !f.split:
		return f.arr, f.dep + cfg.Buffer, true
	case seg == SegDeplane && f.split:
		return f.arr, f.arr + cfg.DeplaneDur, true
	case seg == SegBoarding && f.split:
		return f.dep - cfg.BoardingDur, f.dep + cfg.Buffer, true
	default:
		return 0, 0, false
	}
}

func (s *System) gateOf(f *flightState, seg Segment) string {
	if seg == SegBoarding {
		return f.boardingGate
	}
	return f.deplaneGate
}

func (s *System) setGate(f *flightState, seg Segment, g string) {
	if seg == SegBoarding {
		f.boardingGate = g
	} else {
		f.deplaneGate = g
	}
}

// lockedForAssign 报告该段登机口在 now 是否已经不可再改。
func lockedForAssign(now int, f *flightState, seg Segment) bool {
	if seg == SegBoarding {
		return now >= f.dep-f.spec.BoardLead
	}
	return now >= f.arr
}

// protected 报告延误裁决中某段是否不可被挤占。
func protected(now int, f *flightState, seg Segment) bool {
	if seg == SegBoarding {
		return now >= f.dep-f.spec.BoardLead
	}
	// 整段航班：已到达即不可挤；两段航班的卸客段只看是否已到达。
	if seg == SegWhole {
		return now >= f.arr || now >= f.dep-f.spec.BoardLead
	}
	return now >= f.arr
}

func assignFail(r Reason, msg, conflict string) AssignResult {
	return AssignResult{Err: &OpError{Reason: r, Message: msg, ConflictID: conflict}}
}

func delayFail(r Reason, msg string) DelayResult {
	return DelayResult{Err: &OpError{Reason: r, Message: msg}}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Assign 把航班（整段）或某段指派到登机口；对已有指派即改派。
func (s *System) Assign(now int, flightID, gateID string, seg Segment) AssignResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if flightID == "" || gateID == "" || seg < SegWhole || seg > SegBoarding {
		return assignFail(RejInvalidParam, "assign: empty id or bad segment", "")
	}
	if now < 0 {
		return assignFail(RejInvalidParam, "assign: negative time", "")
	}
	if now < s.now {
		return assignFail(RejClockBack, "assign: clock moved backwards", "")
	}
	f := s.flights[flightID]
	g := s.gates[gateID]
	if f == nil || g == nil {
		return assignFail(RejNotFound, "assign: unknown flight or gate", "")
	}
	validSeg := (!f.split && seg == SegWhole) || (f.split && (seg == SegDeplane || seg == SegBoarding))
	if !validSeg {
		return assignFail(RejInvalidParam, "assign: segment not applicable to split state", "")
	}
	if lockedForAssign(now, f, seg) {
		return assignFail(RejArrivedLocked, "assign: flight arrived or boarding started", "")
	}
	if !classOK(f.spec.Class, g.spec.MaxClass) {
		return assignFail(RejClassIncompat, "assign: class exceeds gate max", "")
	}
	if !kindOK(f.spec.Kind, g.spec.Kind) {
		return assignFail(RejKindMismatch, "assign: intl/domestic mismatch", "")
	}
	start, end, _ := segInterval(s.cfg, f, seg)

	oldGate := s.gateOf(f, seg)
	var oldOcc *treeOcc
	if oldGate != "" {
		os, oe, _ := segInterval(s.cfg, f, seg)
		s.gates[oldGate].tree.delete(f.spec.ID, seg, os)
		oldOcc = &treeOcc{flight: f.spec.ID, seg: seg, start: os, end: oe}
	}

	if f.spec.Class == Class3 {
		for _, nID := range sortedKeys(g.adj) {
			for _, o := range s.gates[nID].tree.overlaps(start, end) {
				if o.flight == flightID {
					continue
				}
				if s.flights[o.flight].spec.Class == Class3 {
					s.restoreOld(oldGate, oldOcc)
					return assignFail(RejAdjacency, "assign: class-3 neighbor occupied by class-3", "")
				}
			}
		}
	}
	for _, o := range g.tree.overlaps(start, end) {
		if o.flight == flightID {
			continue
		}
		s.restoreOld(oldGate, oldOcc)
		return assignFail(RejTimeConflict, "assign: gate occupied", o.flight)
	}

	g.tree.insert(&treeOcc{flight: f.spec.ID, seg: seg, start: start, end: end})
	s.setGate(f, seg, gateID)
	s.now = now
	return AssignResult{OK: true}
}

func (s *System) restoreOld(gateID string, old *treeOcc) {
	if gateID != "" && old != nil {
		s.gates[gateID].tree.insert(old)
	}
}

func (s *System) occupiedSegs(f *flightState) []Segment {
	if !f.split {
		return []Segment{SegWhole}
	}
	return []Segment{SegDeplane, SegBoarding}
}

// removedOcc 记录临时从区间树中移除的占用，用于回滚。
type removedOcc struct {
	gate string
	occ  *treeOcc
}

// Delay 更新航班的到达/起飞时刻（仅允许推迟）。
func (s *System) Delay(now int, flightID string, mask DelayMask, newArr, newDep int) DelayResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if flightID == "" || mask == 0 || mask&^(MaskArr|MaskDep) != 0 || newArr < 0 || newDep < 0 {
		return delayFail(RejInvalidParam, "delay: bad arguments")
	}
	if now < 0 {
		return delayFail(RejInvalidParam, "delay: negative time")
	}
	if now < s.now {
		return delayFail(RejClockBack, "delay: clock moved backwards")
	}
	f := s.flights[flightID]
	if f == nil {
		return delayFail(RejNotFound, "delay: unknown flight")
	}
	na, nd := f.arr, f.dep
	if mask&MaskArr != 0 {
		if newArr < f.arr {
			return delayFail(RejInvalidParam, "delay: arrival can only be postponed")
		}
		na = newArr
	}
	if mask&MaskDep != 0 {
		if newDep < f.dep {
			return delayFail(RejInvalidParam, "delay: departure can only be postponed")
		}
		nd = newDep
	}
	if na > nd {
		return delayFail(RejInvalidParam, "delay: arrival after departure")
	}
	// 已到达则到达时刻/到达登机口不可改；已开始登机则起飞时刻不可改。
	if mask&MaskArr != 0 && now >= f.arr {
		return delayFail(RejArrivedLocked, "delay: flight already arrived")
	}
	if mask&MaskDep != 0 && now >= f.dep-f.spec.BoardLead {
		return delayFail(RejArrivedLocked, "delay: boarding already started")
	}

	backup := *f
	var evictions []Eviction
	// 挤占可能改动任意第三方航班的指派字段；操作前对全部航班深备份，
	// 失败时整笔恢复，保证“不留任何改动”。
	flightBak := map[string]flightState{}
	for id, fl := range s.flights {
		flightBak[id] = *fl
	}
	// 同时备份所有登机口占用。占用数通常很小；深拷贝每棵树的全部条目。
	treeBak := map[string][]*treeOcc{}
	for id, gs := range s.gates {
		var occs []*treeOcc
		var walk func(*treapNode)
		walk = func(h *treapNode) {
			if h == nil {
				return
			}
			walk(h.left)
			oc := *h.occ
			occs = append(occs, &oc)
			walk(h.right)
		}
		walk(gs.tree.root)
		treeBak[id] = occs
	}
	for _, seg := range s.occupiedSegs(f) {
		if g0 := s.gateOf(f, seg); g0 != "" {
			os, _, _ := segInterval(s.cfg, f, seg)
			s.gates[g0].tree.delete(flightID, seg, os)
		}
	}

	wasSplit := f.split
	f.arr, f.dep = na, nd
	f.split = s.cfg.wholeLen(na, nd) > s.cfg.MaxStay
	becameSplit := !wasSplit && f.split
	if wasSplit != f.split {
		// 整段<->两段转换：原（卸客段）登机口保留为到达侧登机口，
		// 成为登机段的一侧转为待分配。
		f.deplaneGate = backup.deplaneGate
		f.boardingGate = ""
	}

	rollback := func(r Reason, msg string) DelayResult {
		for id, fl := range flightBak {
			*s.flights[id] = fl
		}
		for id, occs := range treeBak {
			t := &occTree{}
			for _, o := range occs {
				oc := *o
				t.insert(&oc)
			}
			s.gates[id].tree = t
		}
		return delayFail(r, msg)
	}

	for _, seg := range s.occupiedSegs(f) {
		gateID := s.gateOf(f, seg)
		if gateID == "" {
			continue
		}
		g := s.gates[gateID]
		start, end, _ := segInterval(s.cfg, f, seg)

		type clash struct {
			flight string
			seg    Segment
			gate   string
			start  int
			cause  Reason
		}
		var clashes []clash
		for _, o := range g.tree.overlaps(start, end) {
			if o.flight != flightID {
				clashes = append(clashes, clash{o.flight, o.seg, gateID, o.start, RejTimeConflict})
			}
		}
		if f.spec.Class == Class3 {
			for _, nID := range sortedKeys(g.adj) {
				for _, o := range s.gates[nID].tree.overlaps(start, end) {
					if o.flight != flightID && s.flights[o.flight].spec.Class == Class3 {
						clashes = append(clashes, clash{o.flight, o.seg, nID, o.start, RejAdjacency})
					}
				}
			}
		}
		sort.SliceStable(clashes, func(i, j int) bool {
			if clashes[i].start != clashes[j].start {
				return clashes[i].start < clashes[j].start
			}
			if clashes[i].flight != clashes[j].flight {
				return clashes[i].flight < clashes[j].flight
			}
			if clashes[i].seg != clashes[j].seg {
				return clashes[i].seg < clashes[j].seg
			}
			return clashes[i].gate < clashes[j].gate
		})

		for _, c := range clashes {
			other := s.flights[c.flight]
			// 冲突可能已因前序挤占而消失。
			still := false
			if c.cause == RejTimeConflict {
				os, oe, _ := segInterval(s.cfg, other, c.seg)
				still = s.gateOf(other, c.seg) == c.gate && overlap(start, end, os, oe)
			} else {
				os, oe, _ := segInterval(s.cfg, other, c.seg)
				still = s.gateOf(other, c.seg) == c.gate && overlap(start, end, os, oe)
			}
			if !still {
				continue
			}

			moverWins := higherPriority(f.spec.Kind, f.spec.Class, f.spec.SchedArr, flightID,
				other.spec.Kind, other.spec.Class, other.spec.SchedArr, c.flight)
			if moverWins {
				if protected(now, other, c.seg) {
					return rollback(RejCannotEvict, "delay: opponent cannot be evicted")
				}
				s.evict(c.flight, c.seg, c.gate)
				evictions = append(evictions, Eviction{
					Victim: c.flight, Seg: c.seg, Gate: c.gate, Winner: flightID, Cause: c.cause,
				})
			} else {
				if protected(now, f, seg) {
					return rollback(RejCannotEvict, "delay: moving flight cannot be evicted")
				}
				s.evict(flightID, seg, gateID)
				evictions = append(evictions, Eviction{
					Victim: flightID, Seg: seg, Gate: gateID, Winner: c.flight, Cause: c.cause,
				})
				gateID = "" // 本航班该段已失去登机口，后续冲突不再处理
				break
			}
		}
	}

	for _, seg := range s.occupiedSegs(f) {
		if gateID := s.gateOf(f, seg); gateID != "" {
			start, end, _ := segInterval(s.cfg, f, seg)
			s.gates[gateID].tree.insert(&treeOcc{flight: flightID, seg: seg, start: start, end: end})
		}
	}

	s.now = now
	sort.SliceStable(evictions, func(i, j int) bool {
		if evictions[i].Victim != evictions[j].Victim {
			return evictions[i].Victim < evictions[j].Victim
		}
		return evictions[i].Seg < evictions[j].Seg
	})
	return DelayResult{OK: true, BecameSplit: becameSplit, Evicted: evictions}
}

// evict 令某航班某段失去登机口（转待分配，不自动重指派）。
func (s *System) evict(flightID string, seg Segment, gateID string) {
	f := s.flights[flightID]
	st, _, ok := segInterval(s.cfg, f, seg)
	if !ok {
		return
	}
	s.gates[gateID].tree.delete(flightID, seg, st)
	s.setGate(f, seg, "")
}

// OccupantAt 返回某登机口在某时刻的占用者（航班标识与段）；空串表示空闲。
// 开销为该登机口区间树的 O(log n)，与历史占用总数无关。
func (s *System) OccupantAt(gateID string, t int) (string, Segment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gates[gateID]
	if !ok {
		return "", SegWhole, false
	}
	for _, o := range g.tree.overlaps(t, t+1) {
		return o.flight, o.seg, true
	}
	return "", SegWhole, false
}

// Now 返回最近一次被接受操作的时刻。
func (s *System) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// IsSplit 报告航班当前是否为两段占用。
func (s *System) IsSplit(flightID string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flights[flightID]
	if !ok {
		return false, false
	}
	return f.split, true
}

// GateOf 返回航班整段/卸客段、登机段的当前登机口。
func (s *System) GateOf(flightID string) (arrivalGate, boardingGate string, split bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, exists := s.flights[flightID]
	if !exists {
		return "", "", false, false
	}
	return f.deplaneGate, f.boardingGate, f.split, true
}

// Snapshot 返回完整确定性快照，供朴素模型对拍。
func (s *System) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *System) snapshotLocked() Snapshot {
	snap := Snapshot{Now: s.now, Flights: map[string]FlightState{}}
	for id, f := range s.flights {
		snap.Flights[id] = FlightState{
			Arr:          f.arr,
			Dep:          f.dep,
			Split:        f.split,
			DeplaneGate:  f.deplaneGate,
			BoardingGate: f.boardingGate,
		}
	}
	for _, f := range s.flights {
		segs := s.occupiedSegs(f)
		for _, seg := range segs {
			g := s.gateOf(f, seg)
			if g == "" {
				continue
			}
			start, end, _ := segInterval(s.cfg, f, seg)
			snap.Assignments = append(snap.Assignments, Assignment{
				Flight: f.spec.ID, Seg: seg, Gate: g, Start: start, End: end,
			})
		}
	}
	sort.Slice(snap.Assignments, func(i, j int) bool {
		if snap.Assignments[i].Flight != snap.Assignments[j].Flight {
			return snap.Assignments[i].Flight < snap.Assignments[j].Flight
		}
		return snap.Assignments[i].Seg < snap.Assignments[j].Seg
	})
	return snap
}
