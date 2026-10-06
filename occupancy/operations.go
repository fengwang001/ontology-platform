package occupancy

import (
	"fmt"
	"sort"
)

// Apply 受理一份许可申请（常规或应急）。
func (s *Service) Apply(req ApplyRequest) ApplyResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Priority != Regular && req.Priority != Emergency {
		return ApplyResult{Reason: fail(ErrInvalidParams, "unknown priority")}
	}
	if req.Lanes <= 0 || req.Start < 0 || req.End <= req.Start {
		return ApplyResult{Reason: fail(ErrInvalidParams, "lanes/start/end invalid")}
	}
	if req.OpTime < s.clock {
		return ApplyResult{Reason: fail(ErrClockRollback, fmt.Sprintf("op %d < clock %d", req.OpTime, s.clock))}
	}
	roadLanes, ok := s.lanes[req.Road]
	if !ok {
		return ApplyResult{Reason: fail(ErrRoadNotFound, req.Road)}
	}
	if req.Lanes > roadLanes {
		return ApplyResult{Reason: fail(ErrLanesExceed, fmt.Sprintf("%d > %d on %s", req.Lanes, roadLanes, req.Road))}
	}
	if req.Start < req.OpTime {
		return ApplyResult{Reason: fail(ErrStartBeforeNow, fmt.Sprintf("start %d < op %d", req.Start, req.OpTime))}
	}

	iv := Interval{req.Start, req.End}
	cand := piece{
		road: req.Road, start: iv.Start, end: iv.End, lanes: req.Lanes,
		full: req.Lanes == roadLanes, corr: s.corr[req.Road], priority: req.Priority,
	}
	var cr checkResult
	var held []*Permit
	var preempted []PreemptRecord
	var emKey int64
	if req.Priority == Emergency {
		// 应急只与其他应急比车道数：先收集同路段相交的常规已批许可并摘掉，
		// 再检查应急之间的车道约束；检查失败原样放回，被拒操作不改变状态。
		idx := s.indices[req.Road]
		for _, pc := range idx.intersecting(iv) {
			if pc.priority == Regular {
				if p := s.permits[pc.permitID]; p.Status == Approved {
					held = append(held, p)
					idx.remove(pc.key)
					p.curKey = 0
				}
			}
		}
		cr = s.emergencyCheck(eval{candidate: cand})
		if cr.fail() {
			for _, h := range held {
				pc := s.makePiece(h, h.Interval, 0)
				h.curKey = idx.insert(pc)
			}
		}
	} else {
		cr = s.regularCheck(eval{candidate: cand})
	}
	if cr.fail() {
		s.logf("APPLY REJECT road=%s [%d,%d) lanes=%d prio=%s reason=%s",
			req.Road, req.Start, req.End, req.Lanes, req.Priority, cr.why)
		return ApplyResult{Reason: cr.err()}
	}

	opSeq := s.nextSeq()
	s.clock = req.OpTime

	if req.Priority == Emergency {
		emID := s.nextID + 1
		cand.permitID = emID
		preempted, emKey = s.executePreemption(cand, held, opSeq, req.OpTime)
	}

	p := &Permit{
		ID: s.nextID + 1, Road: req.Road, Lanes: req.Lanes, Interval: iv,
		Priority: req.Priority, Status: Approved, ApprovedSeq: opSeq,
	}
	s.nextID++
	if req.Priority != Emergency {
		idx := s.indices[req.Road]
		pc := s.makePiece(p, iv, 0)
		p.curKey = idx.insert(pc)
	} else {
		p.curKey = emKey
	}
	s.permits[p.ID] = p
	s.recordChange(p, Approved, iv, opSeq, req.OpTime, "applied:"+req.Priority.String())

	s.logf("APPLY ACCEPT id=%d road=%s [%d,%d) lanes=%d prio=%s preempted=%d",
		p.ID, req.Road, req.Start, req.End, req.Lanes, req.Priority, len(preempted))
	s.events = append(s.events, s.applyEvent(opSeq, req, p, preempted))
	return ApplyResult{PermitID: p.ID, Permit: *p, Preempted: preempted}
}

// executePreemption 对同一路段与应急时段相交的常规已批许可执行抢占：
// 未开始整体顺延；已开始截断、前段归档、剩余顺延。随后按原批准时刻先后
// 依次重新审查，先处理者的结果影响后处理者。
func (s *Service) executePreemption(em piece, victims []*Permit, opSeq, opTime int64) ([]PreemptRecord, int64) {
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].ApprovedSeq != victims[j].ApprovedSeq {
			return victims[i].ApprovedSeq < victims[j].ApprovedSeq
		}
		return victims[i].ID < victims[j].ID
	})

	records := make([]PreemptRecord, 0, len(victims))
	// 应急许可即将占用 [em.start,em.end)：重审顺延许可时它必须作为既存占用
	// 参与同路段判定（应急豁免绕行/走廊，所以只需要让它进入同路段求和）。
	idx := s.indices[em.road]
	emKey := idx.insert(em)
	for _, p := range victims {
		rec := PreemptRecord{PermitID: p.ID}
		old := p.Interval
		// 受理阶段已将该许可的当前占用摘除，这里直接处理顺延。

		var newStart, newEnd int64
		if old.Start < em.start {
			// 已开始：截断，[old.Start, em.start) 归档为历史事实。
			rec.Truncated = true
			head := piece{
				permitID: p.ID, road: p.Road, start: old.Start, end: em.start,
				lanes: p.Lanes, full: p.Lanes == s.lanes[p.Road],
				corr: s.corr[p.Road], priority: Regular, approved: true,
			}
			s.indices[p.Road].archivePiece(head)
			newStart = em.end
			newEnd = em.end + (old.End - em.start)
		} else {
			newStart = old.Start
			if newStart < em.end {
				newStart = em.end
			}
			newEnd = newStart + (old.End - old.Start)
		}
		rec.NewStart, rec.NewEnd = newStart, newEnd

		cand := piece{
			road: p.Road, start: newStart, end: newEnd, lanes: p.Lanes,
			full: p.Lanes == s.lanes[p.Road], corr: s.corr[p.Road], priority: Regular,
		}
		cr := s.regularCheck(eval{candidate: cand})
		p.Interval = Interval{newStart, newEnd}
		if cr.fail() {
			rec.Reason = cr.err()
			s.recordChange(p, PendingReschedule, p.Interval, opSeq, opTime,
				"preempted:reschedule-failed:"+cr.why)
			s.logf("PREEMPT id=%d truncated=%v shift=[%d,%d) REJECT reason=%s",
				p.ID, rec.Truncated, newStart, newEnd, cr.why)
		} else {
			rec.Approved = true
			pc := s.makePiece(p, p.Interval, 0)
			p.curKey = s.indices[p.Road].insert(pc)
			s.recordChange(p, Approved, p.Interval, opSeq, opTime, "preempted:rescheduled")
			s.logf("PREEMPT id=%d truncated=%v shift=[%d,%d) ACCEPT",
				p.ID, rec.Truncated, newStart, newEnd)
		}
		records = append(records, rec)
	}
	return records, emKey
}

// Extend 变更已批许可的截止时刻；缩短无需审查，延长按新申请规则审查，
// 审查时把本许可原时段视为已存在（从冲突集合中排除自身）。
func (s *Service) Extend(req ExtendRequest) MutationResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.OpTime < s.clock {
		return MutationResult{Reason: fail(ErrClockRollback,
			fmt.Sprintf("op %d < clock %d", req.OpTime, s.clock))}
	}
	p, ok := s.permits[req.PermitID]
	if !ok {
		return MutationResult{Reason: fail(ErrPermitNotFound, fmt.Sprintf("%d", req.PermitID))}
	}
	if p.Status != Approved || req.OpTime >= p.Interval.End {
		return MutationResult{Reason: fail(ErrPermitEnded,
			fmt.Sprintf("id=%d status=%s end=%d", p.ID, p.Status, p.Interval.End))}
	}
	if req.NewEnd <= p.Interval.Start {
		return MutationResult{Reason: fail(ErrInvalidParams, "new end must be after start")}
	}

	opSeq := s.nextSeq()
	old := p.Interval

	if req.NewEnd <= old.End {
		s.replaceLive(p, Interval{old.Start, req.NewEnd})
		p.Interval.End = req.NewEnd
		s.clock = req.OpTime
		s.recordChange(p, Approved, p.Interval, opSeq, req.OpTime, "extend:shortened")
		s.logf("EXTEND ACCEPT id=%d shorten end=%d", p.ID, req.NewEnd)
		s.events = append(s.events, s.extendEvent(opSeq, req, p, true, CodeError{}))
		return MutationResult{Permit: *p}
	}

	// 延长部分按新申请规则审查：候选只取新增的 [old.End, NewEnd)，
	// 本许可原时段天然视为已存在且无需再与新部分比较。
	cand := piece{
		road: p.Road, start: old.End, end: req.NewEnd, lanes: p.Lanes,
		full: p.Lanes == s.lanes[p.Road], corr: s.corr[p.Road],
		priority: p.Priority,
	}
	var cr checkResult
	if p.Priority == Emergency {
		cr = s.sameRoadCheck(eval{candidate: cand})
	} else {
		cr = s.regularCheck(eval{candidate: cand})
	}
	if cr.fail() {
		// 延期被拒不改变原许可，也不推进时钟。
		s.seq--
		s.logf("EXTEND REJECT id=%d newEnd=%d reason=%s", p.ID, req.NewEnd, cr.why)
		return MutationResult{Reason: cr.err()}
	}

	s.replaceLive(p, Interval{old.Start, req.NewEnd})
	p.Interval.End = req.NewEnd
	s.clock = req.OpTime
	s.recordChange(p, Approved, p.Interval, opSeq, req.OpTime, "extend:lengthened")
	s.logf("EXTEND ACCEPT id=%d end=%d->%d", p.ID, old.End, req.NewEnd)
	s.events = append(s.events, s.extendEvent(opSeq, req, p, true, CodeError{}))
	return MutationResult{Permit: *p}
}

// replaceLive 用新区间替换许可在索引中的片段。旧片段中位于新区间之前的
// 部分作为历史事实归档（只可能发生在缩短/抢占后的延期）。
func (s *Service) replaceLive(p *Permit, iv Interval) {
	idx := s.indices[p.Road]
	if p.curKey != 0 {
		idx.remove(p.curKey)
		p.curKey = 0
	}
	pc := s.makePiece(p, iv, 0)
	p.curKey = idx.insert(pc)
}

// Revoke 撤销一份未结束的许可；待重排的许可也可撤销。
func (s *Service) Revoke(req RevokeRequest) MutationResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.OpTime < s.clock {
		return MutationResult{Reason: fail(ErrClockRollback,
			fmt.Sprintf("op %d < clock %d", req.OpTime, s.clock))}
	}
	p, ok := s.permits[req.PermitID]
	if !ok {
		return MutationResult{Reason: fail(ErrPermitNotFound, fmt.Sprintf("%d", req.PermitID))}
	}
	if p.Status == Revoked {
		return MutationResult{Reason: fail(ErrPermitEnded, "already revoked")}
	}
	// 已结束（时段已过）的已批许可不可撤销；待重排许可没有确定的占用，
	// 其“未结束”以状态为准，允许撤销。
	if p.Status == Approved && req.OpTime >= p.Interval.End {
		return MutationResult{Reason: fail(ErrPermitEnded,
			fmt.Sprintf("id=%d ended at %d", p.ID, p.Interval.End))}
	}

	opSeq := s.nextSeq()
	if p.curKey != 0 {
		idx := s.indices[p.Road]
		idx.remove(p.curKey)
		p.curKey = 0
	} else {
		// 待重排许可没有当前占用；可能存在截断归档的前段。
	}
	s.clock = req.OpTime
	s.recordChange(p, Revoked, p.Interval, opSeq, req.OpTime, "revoked")
	s.logf("REVOKE ACCEPT id=%d", p.ID)
	s.events = append(s.events, s.revokeEvent(opSeq, req, p))
	return MutationResult{Permit: *p}
}

// Query 查询路段在某时刻的封闭车道数与生效许可。不读取也不推进时钟。
func (s *Service) Query(req QueryRequest) (QueryResult, CodeError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.indices[req.Road]
	if !ok {
		return QueryResult{}, fail(ErrRoadNotFound, req.Road)
	}
	ps := idx.pointAt(req.At)
	sort.Slice(ps, func(i, j int) bool { return ps[i].permitID < ps[j].permitID })
	res := QueryResult{}
	seen := map[int64]bool{}
	for _, pc := range ps {
		if seen[pc.permitID] {
			continue
		}
		seen[pc.permitID] = true
		p := s.permits[pc.permitID]
		if p.Status == Revoked {
			seen[pc.permitID] = false
			continue
		}
		res.ClosedLanes += pc.lanes
		res.Active = append(res.Active, Permit{
			ID: p.ID, Road: p.Road, Lanes: p.Lanes,
			Interval: Interval{pc.start, pc.end},
			Priority: p.Priority, Status: p.Status, ApprovedSeq: p.ApprovedSeq,
		})
	}
	return res, CodeError{}
}

func (s *Service) applyEvent(seq int64, req ApplyRequest, p *Permit, preempted []PreemptRecord) Event {
	return Event{
		Seq: seq, OpTime: req.OpTime, Kind: EventApply,
		Road: req.Road, Lanes: req.Lanes, Start: req.Start, End: req.End,
		Priority: req.Priority, PermitID: p.ID, Accepted: true,
		Permit: *p, Preempted: preempted,
	}
}

func (s *Service) extendEvent(seq int64, req ExtendRequest, p *Permit, accepted bool, reason CodeError) Event {
	return Event{
		Seq: seq, OpTime: req.OpTime, Kind: EventExtend,
		PermitID: req.PermitID, NewEnd: req.NewEnd,
		Accepted: accepted, Reason: reason, Permit: *p,
	}
}

func (s *Service) revokeEvent(seq int64, req RevokeRequest, p *Permit) Event {
	return Event{
		Seq: seq, OpTime: req.OpTime, Kind: EventRevoke,
		PermitID: req.PermitID, Accepted: true, Permit: *p,
	}
}
