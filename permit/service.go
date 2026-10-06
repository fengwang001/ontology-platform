package permit

import (
	"sort"
	"sync"
)

// Service 是占道施工许可审查服务。所有方法并发安全，等价于某一串行顺序。
type Service struct {
	mu sync.Mutex

	net *networkView

	permits map[string]*Permit

	// segIndex 路段 ID -> 该路段全部许可节点的 treap（含历史与墓碑）。
	segIndex map[string]*activeIndex
	// corrIndex 走廊 ID -> 该走廊常规许可节点的 treap。
	corrIndex map[string]*activeIndex

	clock    Time
	clockSet bool
	orderSeq int64

	operations []OperationRecord
}

// NewService 以静态路网与走廊上限构建服务。
func NewService(n Network, corridorCap map[string]int) (*Service, error) {
	v, err := buildNetwork(n, corridorCap)
	if err != nil {
		return nil, err
	}
	s := &Service{
		net:       v,
		permits:   map[string]*Permit{},
		segIndex:  map[string]*activeIndex{},
		corrIndex: map[string]*activeIndex{},
	}
	for id := range v.segments {
		s.segIndex[id] = &activeIndex{}
	}
	for c := range v.corridorCap {
		s.corrIndex[c] = &activeIndex{}
	}
	return s, nil
}

func (s *Service) examiner() *examiner {
	return &examiner{net: s.net, segIndex: s.segIndex, corrIndex: s.corrIndex, permits: s.permits}
}

// Apply 受理一份许可申请。拒绝时不改变任何状态与时钟。
func (s *Service) Apply(req ApplyRequest) *AcceptResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := &AcceptResult{ID: req.ID}

	// 拒绝原因严格优先：参数 > 时钟 > 路段 > 车道超限 > 同路段 > 绕行 > 走廊 > 起始早于当前。
	if req.ID == "" || req.Lanes <= 0 || req.Start >= req.End || !req.Priority.valid() {
		res.Err = ruleErr(ErrInvalidParam, "apply requires non-empty id, positive lanes and valid interval")
		return res
	}
	if s.clockSet && req.OpAt < s.clock {
		res.Err = ruleErr(ErrClockRollback, "operation time before last accepted time")
		return res
	}
	seg, ok := s.net.segments[req.Segment]
	if !ok {
		res.Err = ruleErr(ErrSegmentNotFound, "unknown segment", req.Segment)
		return res
	}
	if _, dup := s.permits[req.ID]; dup {
		res.Err = ruleErr(ErrInvalidParam, "permit id already exists", req.ID)
		return res
	}
	if req.Lanes > seg.Lanes {
		res.Err = ruleErr(ErrLanesExceed,
			"requested lanes "+itoa(req.Lanes)+" > segment lanes "+itoa(seg.Lanes))
		return res
	}

	cand := s.newPermitFromApply(req, seg)

	var targets []*Permit
	if req.Priority == Regular {
		bound := req.OpAt
		if req.Start < bound {
			bound = req.Start
		}
		ex := s.examiner()
		ex.liveAfter = bound
		if err := ex.checkRegular(cand, ""); err != nil {
			res.Err = err
			return res
		}
	} else {
		// 应急只校验同路段车道数：先模拟抢占常规许可，再判应急间约束。
		bound := req.OpAt
		if req.Start < bound {
			bound = req.Start
		}
		ex := s.examiner()
		ex.liveAfter = bound
		targets = ex.preemptTargets(cand)
		for _, p := range targets {
			s.deactivatePermit(p)
		}
		if err := ex.checkEmergencySameSegment(cand, ""); err != nil {
			for _, p := range targets {
				// 拒绝必须完全回滚：恢复被模拟摘除的目标节点。
				s.reenablePermit(p)
			}
			res.Err = err
			return res
		}
		for _, p := range targets {
			s.reenablePermit(p)
		}
	}
	if req.Start < req.OpAt {
		res.Err = ruleErr(ErrStartBeforeNow, "permit start before operation time")
		return res
	}

	// 接受：推进时钟并落档。
	s.advanceClock(req.OpAt)
	s.orderSeq++
	cand.ApprovedAt = req.OpAt
	cand.ApproveOrder = s.orderSeq
	cand.Original = cand.Current
	cand.Status = StatusApproved
	cand.History = []HistoryEntry{{
		At: req.OpAt, Kind: "ACCEPT", Priority: req.Priority,
		Interval: cand.Current, Detail: "accepted",
	}}
	s.permits[req.ID] = cand
	s.insertPermit(cand)

	note := "accepted"
	if req.Priority == Emergency {
		res.PreemptedIDs, res.Reschedule, res.RescheduledInterval, note =
			s.executeEmergency(cand, targets, req.OpAt)
	}
	s.recordOp(OpApply, req.OpAt, note, &req, nil, nil)
	res.Accepted = true
	return res
}

// reenablePermit 恢复模拟抢占时被置墓碑的节点（仅用于应急受理被拒回滚）。
func (s *Service) reenablePermit(p *Permit) {
	s.segIndex[p.Segment].setValid(p.indexedStart, p.ID, true)
	if p.Priority == Regular {
		if ix := s.corrIndex[p.Corridor]; ix != nil {
			ix.setValid(p.indexedStart, p.ID, true)
		}
	}
}

// Extend 处理延期；缩短无需审查，延长部分按新申请全部规则审查。
func (s *Service) Extend(req ExtendRequest) *RuleError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.ID == "" {
		return ruleErr(ErrInvalidParam, "extend requires permit id")
	}
	if s.clockSet && req.OpAt < s.clock {
		return ruleErr(ErrClockRollback, "operation time before last accepted time")
	}
	p, ok := s.permits[req.ID]
	if !ok {
		return ruleErr(ErrPermitNotFound, "unknown permit", req.ID)
	}
	if p.Status == StatusRevoked {
		return ruleErr(ErrPermitNotFound, "permit revoked", req.ID)
	}
	if p.Status != StatusApproved || p.Current.End <= req.OpAt {
		return ruleErr(ErrPermitEnded, "permit not active, cannot extend", req.ID)
	}
	if req.NewEnd <= p.Current.Start {
		return ruleErr(ErrInvalidParam, "new end must be after original start", req.ID)
	}

	if req.NewEnd <= p.Current.End {
		// 缩短或不变：无需审查；空操作仅推进时钟。
		s.advanceClock(req.OpAt)
		if req.NewEnd < p.Current.End {
			s.deactivatePermit(p)
			p.Current.End = req.NewEnd
			p.Original.End = req.NewEnd
			s.insertPermit(p)
			p.History = append(p.History, HistoryEntry{
				At: req.OpAt, Kind: "EXTEND", Priority: p.Priority,
				Interval: p.Current, Detail: "shortened without review",
			})
		}
		s.recordOp(OpExtend, req.OpAt, "shortened/no-op", nil, &req, nil)
		return nil
	}

	trial := s.ghostPermit(p, Interval{Start: p.Current.Start, End: req.NewEnd})

	var targets []*Permit
	var checkErr *RuleError
	if p.Priority == Regular {
		ex := s.examiner()
		ex.liveAfter = req.OpAt
		checkErr = ex.checkRegular(trial, p.ID)
	} else {
		// 应急延期：延长段新增的相交常规许可同样被抢占；模拟摘除后审查。
		ex := s.examiner()
		ex.liveAfter = req.OpAt
		targets = ex.preemptTargets(
			s.ghostPermit(p, Interval{Start: p.Current.Start, End: req.NewEnd}))
		for _, q := range targets {
			s.deactivatePermit(q)
		}
		// 审查应急间约束时把自身当前时段摘除（候选覆盖它）。
		s.deactivatePermit(p)
		checkErr = ex.checkEmergencySameSegment(trial, p.ID)
		s.reenablePermit(p)
		for _, q := range targets {
			s.reenablePermit(q)
		}
	}
	if checkErr != nil {
		return checkErr // 延期被拒不改变原许可
	}

	s.advanceClock(req.OpAt)
	// 接受延期：以新时段重入索引（旧节点墓碑）。
	if p.Priority == Emergency {
		for _, q := range targets {
			s.deactivatePermit(q)
		}
	}
	s.deactivatePermit(p)
	p.Current.End = req.NewEnd
	p.Original.End = req.NewEnd
	s.insertPermit(p)
	p.History = append(p.History, HistoryEntry{
		At: req.OpAt, Kind: "EXTEND", Priority: p.Priority,
		Interval: p.Current, Detail: "extended after review",
	})
	note := "extended"
	if p.Priority == Emergency && len(targets) > 0 {
		note = s.runPreemption(p, targets, req.OpAt)
	}
	s.recordOp(OpExtend, req.OpAt, note, nil, &req, nil)
	return nil
}

// Revoke 撤销一份未结束许可；被抢占中的许可也可撤销。
func (s *Service) Revoke(req RevokeRequest) *RuleError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.ID == "" {
		return ruleErr(ErrInvalidParam, "revoke requires permit id")
	}
	if s.clockSet && req.OpAt < s.clock {
		return ruleErr(ErrClockRollback, "operation time before last accepted time")
	}
	p, ok := s.permits[req.ID]
	if !ok {
		return ruleErr(ErrPermitNotFound, "unknown permit", req.ID)
	}
	if p.Status == StatusRevoked {
		return ruleErr(ErrPermitNotFound, "permit already revoked", req.ID)
	}
	// 被抢占/待重排许可持有顺延候选时段，允许撤销；已批且时段已过才报已结束。
	if p.Status == StatusApproved && p.Current.End <= req.OpAt {
		return ruleErr(ErrPermitEnded, "permit already ended", req.ID)
	}

	s.advanceClock(req.OpAt)
	if p.Status == StatusApproved {
		s.deactivatePermit(p)
	}
	p.Status = StatusRevoked
	p.History = append(p.History, HistoryEntry{
		At: req.OpAt, Kind: "REVOKE", Priority: p.Priority,
		Interval: p.Current, Detail: "revoked",
	})
	// 撤销不会使先前被它挡下的申请自动通过：拒绝从未落档，须重新提交。
	s.recordOp(OpRevoke, req.OpAt, "revoked", nil, nil, &req)
	return nil
}

// Query 查询某路段在任意时刻（含历史时刻）的封闭车道数与生效许可。
// 开销不随该路段历史许可总数增长：相交枚举为 O(log n + 命中数)，
// 历史节点由子树 maxEnd 与键序剪枝跳过。
func (s *Service) Query(req QueryRequest) QueryResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.net.segments[req.Segment]; !ok {
		return QueryResult{Segment: req.Segment, At: req.At}
	}
	ns := s.segIndex[req.Segment].activeAt(req.At)
	closed := 0
	ids := make([]string, 0, len(ns))
	for _, n := range ns {
		closed += n.lanes
		ids = append(ids, n.id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return s.permits[ids[i]].ApproveOrder < s.permits[ids[j]].ApproveOrder
	})
	return QueryResult{Segment: req.Segment, At: req.At, Closed: closed, ActiveIDs: ids}
}

// Permit 返回许可档案快照（含不可变状态历史）。
func (s *Service) Permit(id string) (*Permit, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.permits[id]
	if !ok {
		return nil, false
	}
	cp := *p
	cp.History = append([]HistoryEntry(nil), p.History...)
	return &cp, true
}

// Operations 返回已接受操作归档拷贝。
func (s *Service) Operations() []OperationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OperationRecord, len(s.operations))
	copy(out, s.operations)
	return out
}

// Clock 返回当前时钟（尚无被接受操作时 ok 为 false）。
func (s *Service) Clock() (Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock, s.clockSet
}
