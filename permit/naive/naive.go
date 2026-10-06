// Package naive 是一个独立编写的朴素全量重判参考模型。
//
// 它与 permit.Service 不共享任何判定代码：不使用 treap、堆或任何增量结构，
// 每次判定都线性重扫全部许可档案；作为随机差分测试的对照预言机。
package naive

import (
	"sort"

	"ontology/permit"
)

type nPermit struct {
	id       string
	segment  string
	lanes    int
	priority permit.Priority
	start    permit.Time
	end      permit.Time
	status   permit.Status
	order    int64
	history  []permit.HistoryEntry
}

type Model struct {
	segments map[string]*permit.Segment
	cap      map[string]int
	detour   map[string][]string
	incoming map[string]map[string]struct{}

	permits  map[string]*nPermit
	orderSeq int64
	clock    permit.Time
	hasClock bool
	ops      []permit.OperationRecord
}

func New(n permit.Network, corridorCap map[string]int) *Model {
	m := &Model{
		segments: map[string]*permit.Segment{},
		cap:      map[string]int{},
		detour:   map[string][]string{},
		incoming: map[string]map[string]struct{}{},
		permits:  map[string]*nPermit{},
	}
	for i := range n.Segments {
		s := n.Segments[i]
		m.segments[s.ID] = &s
	}
	for c, v := range corridorCap {
		m.cap[c] = v
	}
	for id, s := range m.segments {
		m.detour[id] = append([]string(nil), s.Detour...)
		for _, d := range s.Detour {
			if m.incoming[d] == nil {
				m.incoming[d] = map[string]struct{}{}
			}
			m.incoming[d][id] = struct{}{}
		}
	}
	return m
}

func overlap(a0, a1, b0, b1 permit.Time) bool { return a0 < b1 && b0 < a1 }

// now 为当前操作时刻；判定只考虑在操作时刻仍在册（end > now）的许可。
func (m *Model) active(now, start, end permit.Time, regularOnly bool) []*nPermit {
	var out []*nPermit
	for _, p := range m.permits {
		if p.status == permit.StatusApproved && p.end > now &&
			overlap(p.start, p.end, start, end) &&
			(!regularOnly || p.priority == permit.Regular) {
			out = append(out, p)
		}
	}
	return out
}

func mkErr(code permit.RuleCode, msg string, wit ...string) *permit.RuleError {
	return &permit.RuleError{Code: code, Message: msg, Witness: wit}
}

// checkRegular 全量重扫三类规则；exclude 用于延期排除自身。
func (m *Model) checkRegular(now permit.Time, seg string, lanes int, start, end permit.Time, exclude string) *permit.RuleError {
	s := m.segments[seg]
	sum := lanes
	var wit []string
	// 常规候选的同路段约束只在常规许可之间成立；相交应急由抢占机制处理。
	for _, p := range m.active(now, start, end, true) {
		if p.segment == seg && p.id != exclude {
			sum += p.lanes
			wit = append(wit, p.id)
		}
	}
	if sum > s.Lanes {
		return mkErr(permit.ErrSameSegment, "naive same-segment", wit...)
	}

	det := map[string]struct{}{}
	if lanes == s.Lanes {
		for _, d := range m.detour[seg] {
			for _, p := range m.active(now, start, end, true) {
				if p.segment == d && p.id != exclude {
					det[p.id] = struct{}{}
				}
			}
		}
	}
	for up := range m.incoming[seg] {
		upSeg := m.segments[up]
		// 方向 B：上游全封闭（含应急）对本路段构成物理阻塞。
		for _, p := range m.active(now, start, end, false) {
			if p.segment == up && p.id != exclude && p.lanes == upSeg.Lanes {
				det[p.id] = struct{}{}
			}
		}
	}
	if len(det) > 0 {
		ks := make([]string, 0, len(det))
		for k := range det {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return mkErr(permit.ErrDetour, "naive detour", ks...)
	}

	if capv := m.cap[s.Corridor]; capv > 0 {
		type ev struct {
			t permit.Time
			d int
		}
		evs := []ev{{start, 1}, {end, -1}}
		for _, p := range m.active(now, start, end, true) {
			if m.segments[p.segment].Corridor == s.Corridor && p.id != exclude {
				evs = append(evs, ev{p.start, 1}, ev{p.end, -1})
			}
		}
		sort.Slice(evs, func(i, j int) bool {
			if evs[i].t != evs[j].t {
				return evs[i].t < evs[j].t
			}
			return evs[i].d < evs[j].d
		})
		cur, mx := 0, 0
		for _, e := range evs {
			cur += e.d
			if cur > mx {
				mx = cur
			}
		}
		if mx > capv {
			return mkErr(permit.ErrCorridorCap, "naive corridor", s.Corridor)
		}
	}
	return nil
}

// preemptTargets：相交常规许可按批准次序从晚到早摘除至满足车道数，
// 返回按批准次序升序的抢占集合。
func (m *Model) preemptTargets(now permit.Time, seg string, lanes int, start, end permit.Time) []*nPermit {
	var ov []*nPermit
	for _, p := range m.active(now, start, end, false) {
		if p.segment == seg && p.priority == permit.Regular {
			ov = append(ov, p)
		}
	}
	sort.SliceStable(ov, func(i, j int) bool { return ov[i].order > ov[j].order })
	used := lanes
	var targets []*nPermit
	for _, p := range ov {
		if used+p.lanes > m.segments[seg].Lanes {
			targets = append(targets, p)
		} else {
			used += p.lanes
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].order < targets[j].order })
	return targets
}

func (m *Model) emergencyFeasible(now permit.Time, seg string, lanes int, start, end permit.Time) *permit.RuleError {
	sum := lanes
	var wit []string
	for _, p := range m.active(now, start, end, false) {
		if p.segment == seg && p.priority == permit.Emergency {
			sum += p.lanes
			wit = append(wit, p.id)
		}
	}
	if sum > m.segments[seg].Lanes {
		return mkErr(permit.ErrSameSegment, "naive emergency same-segment", wit...)
	}
	return nil
}

func (m *Model) Apply(req permit.ApplyRequest) *permit.AcceptResult {
	res := &permit.AcceptResult{ID: req.ID}
	if req.ID == "" || req.Lanes <= 0 || req.Start >= req.End ||
		(req.Priority != permit.Regular && req.Priority != permit.Emergency) {
		res.Err = mkErr(permit.ErrInvalidParam, "naive invalid param")
		return res
	}
	if m.hasClock && req.OpAt < m.clock {
		res.Err = mkErr(permit.ErrClockRollback, "naive clock rollback")
		return res
	}
	s, ok := m.segments[req.Segment]
	if !ok {
		res.Err = mkErr(permit.ErrSegmentNotFound, "naive segment", req.Segment)
		return res
	}
	if _, dup := m.permits[req.ID]; dup {
		res.Err = mkErr(permit.ErrInvalidParam, "naive duplicate id", req.ID)
		return res
	}
	if req.Lanes > s.Lanes {
		res.Err = mkErr(permit.ErrLanesExceed, "naive lanes exceed")
		return res
	}

	var targets []*nPermit
	if req.Priority == permit.Regular {
		bound := req.OpAt
		if req.Start < bound {
			bound = req.Start
		}
		if err := m.checkRegular(bound, req.Segment, req.Lanes, req.Start, req.End, ""); err != nil {
			res.Err = err
			return res
		}
	} else {
		bound := req.OpAt
		if req.Start < bound {
			bound = req.Start
		}
		targets = m.preemptTargets(bound, req.Segment, req.Lanes, req.Start, req.End)
		for _, p := range targets {
			p.status = permit.StatusPreempted
		}
		if err := m.emergencyFeasible(bound, req.Segment, req.Lanes, req.Start, req.End); err != nil {
			for _, p := range targets {
				p.status = permit.StatusApproved
			}
			res.Err = err
			return res
		}
		for _, p := range targets {
			p.status = permit.StatusApproved
		}
	}
	if req.Start < req.OpAt {
		res.Err = mkErr(permit.ErrStartBeforeNow, "naive start before now")
		return res
	}

	m.clock = req.OpAt
	m.hasClock = true
	m.orderSeq++
	np := &nPermit{
		id: req.ID, segment: req.Segment, lanes: req.Lanes,
		priority: req.Priority, start: req.Start, end: req.End,
		status: permit.StatusApproved, order: m.orderSeq,
		history: []permit.HistoryEntry{{
			At: req.OpAt, Kind: "ACCEPT", Priority: req.Priority,
			Interval: permit.Interval{Start: req.Start, End: req.End}, Detail: "accepted",
		}},
	}
	m.permits[req.ID] = np

	note := "accepted"
	if req.Priority == permit.Emergency {
		res.PreemptedIDs, res.Reschedule, res.RescheduledInterval, note =
			m.runPreemption(np, targets, req.OpAt)
	}
	m.ops = append(m.ops, permit.OperationRecord{
		Seq: int64(len(m.ops)) + 1, Kind: permit.OpApply, At: req.OpAt, Apply: &req, Note: note,
	})
	res.Accepted = true
	return res
}

func (m *Model) runPreemption(em *nPermit, targets []*nPermit, at permit.Time) (
	[]string, map[string]bool, map[string]permit.Interval, string) {
	ids := make([]string, 0, len(targets))
	for _, p := range targets {
		ids = append(ids, p.id)
		p.status = permit.StatusPreempted
		var ns, ne permit.Time
		if p.start >= at {
			ns = em.end
			ne = em.end + (p.end - p.start)
		} else {
			ns = em.end
			ne = em.end + (p.end - at)
		}
		p.start, p.end = ns, ne
		p.history = append(p.history, permit.HistoryEntry{
			At: at, Kind: "PREEMPT", Priority: p.priority,
			Interval: permit.Interval{Start: ns, End: ne}, By: em.id,
		})
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].order < targets[j].order })
	resched := map[string]bool{}
	ivs := map[string]permit.Interval{}
	for _, p := range targets {
		err := m.checkRegular(at, p.segment, p.lanes, p.start, p.end, "")
		kind, detail := "RESCHEDULE_APPROVED", "approved"
		if err != nil {
			p.status = permit.StatusPendingReschedule
			kind, detail = "RESCHEDULE_REJECTED", err.Error()
		} else {
			p.status = permit.StatusApproved
		}
		p.history = append(p.history, permit.HistoryEntry{
			At: at, Kind: kind, Priority: p.priority,
			Interval: permit.Interval{Start: p.start, End: p.end}, By: em.id, Detail: detail,
		})
		resched[p.id] = p.status == permit.StatusApproved
		ivs[p.id] = permit.Interval{Start: p.start, End: p.end}
	}
	return ids, resched, ivs, "accepted"
}

func (m *Model) Extend(req permit.ExtendRequest) *permit.RuleError {
	if req.ID == "" {
		return mkErr(permit.ErrInvalidParam, "naive invalid")
	}
	if m.hasClock && req.OpAt < m.clock {
		return mkErr(permit.ErrClockRollback, "naive clock rollback")
	}
	p, ok := m.permits[req.ID]
	if !ok {
		return mkErr(permit.ErrPermitNotFound, "naive permit", req.ID)
	}
	if p.status == permit.StatusRevoked {
		return mkErr(permit.ErrPermitNotFound, "naive revoked", req.ID)
	}
	if p.status != permit.StatusApproved || p.end <= req.OpAt {
		return mkErr(permit.ErrPermitEnded, "naive ended", req.ID)
	}
	if req.NewEnd <= p.start {
		return mkErr(permit.ErrInvalidParam, "naive bad new end")
	}

	if req.NewEnd <= p.end {
		m.clock = req.OpAt
		m.hasClock = true
		if req.NewEnd < p.end {
			p.end = req.NewEnd
			p.history = append(p.history, permit.HistoryEntry{
				At: req.OpAt, Kind: "EXTEND", Priority: p.priority,
				Interval: permit.Interval{Start: p.start, End: p.end},
			})
		}
		m.ops = append(m.ops, permit.OperationRecord{
			Seq: int64(len(m.ops)) + 1, Kind: permit.OpExtend, At: req.OpAt, Extend: &req,
		})
		return nil
	}

	var targets []*nPermit
	if p.priority == permit.Regular {
		if err := m.checkRegular(req.OpAt, p.segment, p.lanes, p.start, req.NewEnd, p.id); err != nil {
			return err
		}
	} else {
		targets = m.preemptTargets(req.OpAt, p.segment, p.lanes, p.start, req.NewEnd)
		for _, q := range targets {
			q.status = permit.StatusPreempted
		}
		p.status = permit.StatusPreempted
		if err := m.emergencyFeasible(req.OpAt, p.segment, p.lanes, p.start, req.NewEnd); err != nil {
			for _, q := range targets {
				q.status = permit.StatusApproved
			}
			p.status = permit.StatusApproved
			return err
		}
		p.status = permit.StatusApproved
		for _, q := range targets {
			q.status = permit.StatusApproved
		}
	}

	m.clock = req.OpAt
	m.hasClock = true
	oldEnd := p.end
	p.end = req.NewEnd
	p.history = append(p.history, permit.HistoryEntry{
		At: req.OpAt, Kind: "EXTEND", Priority: p.priority,
		Interval: permit.Interval{Start: p.start, End: p.end},
	})
	note := "extended"
	if p.priority == permit.Emergency && len(targets) > 0 {
		em := &nPermit{id: p.id, end: p.end}
		_, _, _, _ = m.runPreemption(em, targets, req.OpAt)
		note = "extended with preemption"
	}
	_ = oldEnd
	m.ops = append(m.ops, permit.OperationRecord{
		Seq: int64(len(m.ops)) + 1, Kind: permit.OpExtend, At: req.OpAt, Extend: &req, Note: note,
	})
	return nil
}

func (m *Model) Revoke(req permit.RevokeRequest) *permit.RuleError {
	if req.ID == "" {
		return mkErr(permit.ErrInvalidParam, "naive invalid")
	}
	if m.hasClock && req.OpAt < m.clock {
		return mkErr(permit.ErrClockRollback, "naive clock rollback")
	}
	p, ok := m.permits[req.ID]
	if !ok {
		return mkErr(permit.ErrPermitNotFound, "naive permit", req.ID)
	}
	if p.status == permit.StatusRevoked {
		return mkErr(permit.ErrPermitNotFound, "naive revoked", req.ID)
	}
	if p.status == permit.StatusApproved && p.end <= req.OpAt {
		return mkErr(permit.ErrPermitEnded, "naive ended", req.ID)
	}
	m.clock = req.OpAt
	m.hasClock = true
	p.status = permit.StatusRevoked
	p.history = append(p.history, permit.HistoryEntry{
		At: req.OpAt, Kind: "REVOKE", Priority: p.priority,
		Interval: permit.Interval{Start: p.start, End: p.end},
	})
	m.ops = append(m.ops, permit.OperationRecord{
		Seq: int64(len(m.ops)) + 1, Kind: permit.OpRevoke, At: req.OpAt, Revoke: &req,
	})
	return nil
}

func (m *Model) Query(req permit.QueryRequest) permit.QueryResult {
	closed := 0
	var ids []string
	type pair struct {
		id    string
		order int64
	}
	var pairs []pair
	for _, p := range m.permits {
		if p.status == permit.StatusApproved && p.segment == req.Segment &&
			p.start <= req.At && req.At < p.end {
			closed += p.lanes
			pairs = append(pairs, pair{p.id, p.order})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].order < pairs[j].order })
	for _, x := range pairs {
		ids = append(ids, x.id)
	}
	return permit.QueryResult{Segment: req.Segment, At: req.At, Closed: closed, ActiveIDs: ids}
}

// Snapshot 返回全部许可的规范状态快照，供差分测试比较。
func (m *Model) Snapshot() map[string]permit.PermitState {
	out := map[string]permit.PermitState{}
	for id, p := range m.permits {
		out[id] = permit.PermitState{
			Segment: p.segment, Lanes: p.lanes, Priority: p.priority,
			Start: p.start, End: p.end, Status: p.status, Order: p.order,
			History: append([]permit.HistoryEntry(nil), p.history...),
		}
	}
	return out
}
