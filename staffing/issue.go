package staffing

import "fmt"

// IssueOffer 发放一份录用通知，成功返回通知 ID 并占用一个编制。
func (s *Service) IssueOffer(now int, candidateID, positionID string, salary, deadline int) (id int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("IssueOffer{now:%d candidate:%q pos:%q salary:%d deadline:%d}",
		now, candidateID, positionID, salary, deadline)
	defer func() {
		if err != nil {
			s.log("IssueOffer", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("IssueOffer", input, true, CodeOK, fmt.Sprintf("offer:%d", id), "offer issued, one headcount occupied")
		}
	}()

	ids, err := s.issueBatchLocked(now, positionID, []BatchItem{{
		CandidateID: candidateID, PositionID: positionID, Salary: salary, Deadline: deadline,
	}}, false)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// IssueBatch 批量发放（同一岗位），全有或全无；任一不满足整批拒绝，
// 返回失败下标最小的项。
func (s *Service) IssueBatch(now int, positionID string, items []BatchItem) (ids []int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("IssueBatch{now:%d pos:%q n:%d items:%v}", now, positionID, len(items), items)
	defer func() {
		if err != nil {
			s.log("IssueBatch", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("IssueBatch", input, true, CodeOK, fmt.Sprintf("offers:%v", ids),
				fmt.Sprintf("all %d offers issued atomically, %d headcount occupied", len(ids), len(ids)))
		}
	}()

	return s.issueBatchLocked(now, positionID, items, true)
}

// issueBatchLocked 同时承载单发（len=1 且下标不回显）与批量逻辑。
func (s *Service) issueBatchLocked(now int, positionID string, items []BatchItem, batch bool) (ids []int64, err error) {
	if positionID == "" {
		return nil, s.wrapIssue(newError(CodeInvalidParam, "position id is empty"), batch, 0)
	}
	if len(items) == 0 {
		return nil, s.wrapIssue(newError(CodeInvalidParam, "empty batch"), batch, 0)
	}

	// 1) 参数非法（下标最小者优先）。
	for i, it := range items {
		if it.CandidateID == "" || it.Salary < 0 || it.Deadline < now ||
			(it.PositionID != "" && it.PositionID != positionID) {
			return nil, s.wrapIssue(newError(CodeInvalidParam,
				"invalid item %d: candidate=%q salary=%d deadline=%d (now=%d)",
				i, it.CandidateID, it.Salary, it.Deadline, now), batch, i)
		}
	}

	// 2) 时钟回退。
	if err := s.advanceClock(now); err != nil {
		return nil, err
	}

	// 3) 岗位 / 候选人不存在（下标最小者优先）。
	p, ok := s.positions[positionID]
	if !ok {
		return nil, newError(CodeNotFound, "position %q not found", positionID)
	}
	for i, it := range items {
		if !s.candidates[it.CandidateID] {
			return nil, s.wrapIssue(newError(CodeNotFound, "candidate %q not found (item %d)",
				it.CandidateID, i), batch, i)
		}
	}

	// 4) 岗位冻结。
	if p.Frozen {
		return nil, newError(CodeFrozen, "position %q is frozen", positionID)
	}

	// 5) 编制已满：先在事务视图中结算该岗位全部当前未决通知，再比较。
	//    发放判定只读取 O(1) 计数；结算遍历规模=未决数（有界于编制总数）。
	tx := &settleTx{}
	posReasons := s.settlePosition(positionID, now, tx)
	occ := s.occupied(positionID)
	if occ+len(items) > p.Headcount {
		failIdx := p.Headcount - occ // 第一个放不下的下标
		if failIdx < 0 {
			failIdx = 0
		}
		if !batch {
			failIdx = 0
		}
		tx.rollback()
		return nil, s.wrapIssue(newError(CodeHeadcountFull,
			"occupied %d + batch %d > headcount %d (first overflow index %d)",
			occ, len(items), p.Headcount, failIdx), batch, failIdx)
	}

	// 6) 薪资带宽 / 例外额度（按发放季度 QuarterOf(now)）。
	quarter := QuarterOf(now)
	excKey := s.exceptionKey(positionID, quarter)
	remaining := 0
	exc := s.exceptionByKey[excKey]
	if exc != nil {
		remaining = exc.Remaining
	}
	outOfBand := []int{}
	for i, it := range items {
		if it.Salary < p.BandLow || it.Salary > p.BandHigh {
			outOfBand = append(outOfBand, i)
		}
	}
	if len(outOfBand) > remaining {
		failIdx := outOfBand[remaining] // 第 remaining+1 个超带宽项无额度可用
		tx.rollback()
		return nil, s.wrapIssue(newError(CodeBandExceeded,
			"salary out of band [%d,%d] in quarter %d with only %d exception(s) left (item %d)",
			p.BandLow, p.BandHigh, quarter, remaining, failIdx), batch, failIdx)
	}

	// 7) 候选人已有未决通知：先惰性结算候选人现有通知，再查跨项重复。
	for _, it := range items {
		if r := s.settleCandidate(it.CandidateID, now, tx); r != "" {
			s.log("lazy-settle",
				fmt.Sprintf("candidate:%q now:%d", it.CandidateID, now), true, CodeOK, "", r)
		}
	}
	seen := map[string]int{}
	for i, it := range items {
		if _, dup := seen[it.CandidateID]; dup {
			tx.rollback()
			return nil, s.wrapIssue(newError(CodePendingExists,
				"candidate %q appears twice in batch and would hold two pending offers (item %d)",
				it.CandidateID, i), batch, i)
		}
		seen[it.CandidateID] = i
		if _, has := s.candidatePending[it.CandidateID]; has {
			tx.rollback()
			return nil, s.wrapIssue(newError(CodePendingExists,
				"candidate %q already has a pending offer; withdraw it first (item %d)",
				it.CandidateID, i), batch, i)
		}
	}

	// 8) 冷却中（下标最小者优先）。
	for i, it := range items {
		if cool, blockDay := s.inCooldown(it.CandidateID, positionID, now); cool {
			tx.rollback()
			return nil, s.wrapIssue(newError(CodeCooldown,
				"candidate %q rejected/abandoned at day %d, cooldown %d, now %d (item %d)",
				it.CandidateID, blockDay, s.cooldown, now, i), batch, i)
		}
	}

	// 全部校验通过：整批落地（全有或全无——此前未做任何状态变更）。
	excUsed := 0
	created := make([]*Offer, 0, len(items))
	for _, it := range items {
		s.nextOfferID++
		useExc := it.Salary < p.BandLow || it.Salary > p.BandHigh
		o := &Offer{
			ID: s.nextOfferID, CandidateID: it.CandidateID, PositionID: positionID,
			Salary: it.Salary, Deadline: it.Deadline, IssuedAt: now,
			Status: StatusPending, RespondedAt: -1, EntryDate: -1,
			OnboardedAt: -1, LeftAt: -1, CanceledAt: -1, UsedException: useExc,
		}
		s.offers[o.ID] = o
		s.addPending(o)
		if useExc {
			if exc == nil {
				return nil, newError(CodeBandExceeded, "internal: exception missing for %s", excKey)
			}
			exc.Remaining--
			excUsed++
		}
		created = append(created, o)
	}
	s.commitClock(now)
	for _, r := range posReasons {
		s.log("lazy-settle", fmt.Sprintf("pos:%q now:%d", positionID, now), true, CodeOK, "", r)
	}

	ids = make([]int64, len(created))
	for i, o := range created {
		ids[i] = o.ID
	}
	if excUsed > 0 {
		s.log("exception-use",
			fmt.Sprintf("pos:%q quarter:%d n:%d", positionID, quarter, excUsed),
			true, CodeOK, "", fmt.Sprintf("%d exception(s) consumed on issuance success", excUsed))
	}
	return ids, nil
}

func (s *Service) wrapIssue(err *Error, batch bool, index int) error {
	if batch {
		return newBatchError(err.Code, index, "%s", err.Msg)
	}
	return err
}
