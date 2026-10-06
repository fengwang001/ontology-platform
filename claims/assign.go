package claims

// Assign 将案件的指定空位分配给指定复核员。
// 拒绝次序：参数非法 > 案件不存在 > 复核员不存在 > 已终态 > 利益冲突 > 已分配。
func (e *Engine) Assign(caseID string, slotIdx int, reviewerID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" || reviewerID == "" || slotIdx < 0 {
		return ErrInvalidParam
	}
	c, ok := e.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	r, ok := e.reviewers[reviewerID]
	if !ok {
		return ErrReviewerNotFound
	}
	e.sweepCaseLocked(c)
	if c.final() {
		return ErrCaseFinal
	}
	if slotIdx >= len(c.slots) || (slotIdx > 0 && !c.slots[slotIdx-1].assigned) {
		return ErrInvalidParam
	}
	if r.Branch == c.branch {
		return ErrConflict
	}
	if c.slots[slotIdx].assigned {
		return ErrSlotOccupied
	}
	if err := eligibility(c, slotIdx, r); err != nil {
		return err
	}
	e.assignSlotLocked(c, slotIdx, reviewerID)
	return nil
}

// RequestAssign 申请分配：取受理时刻最早的未分配案件（堆顶，O(1)），
// 分配给其最小的未分配空位。等待中的案件数为零报“无待办”。
func (e *Engine) RequestAssign(reviewerID string) (string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if reviewerID == "" {
		return "", 0, ErrInvalidParam
	}
	r, ok := e.reviewers[reviewerID]
	if !ok {
		return "", 0, ErrReviewerNotFound
	}
	if len(e.pending) == 0 {
		return "", 0, ErrNoPending
	}
	c := e.cases[e.pending[0].caseID]
	e.sweepCaseLocked(c)
	if r.Branch == c.branch {
		return "", 0, ErrConflict
	}
	idx := firstOpenSlot(c)
	if idx < 0 {
		// 不变式：堆中案件必有未分配空位；兜底防脏数据。
		e.dequeueLocked(c.id)
		return "", 0, ErrNoPending
	}
	if err := eligibility(c, idx, r); err != nil {
		return "", 0, err
	}
	e.assignSlotLocked(c, idx, reviewerID)
	return c.id, idx, nil
}

// assignSlotLocked 分配空位并记录分配时刻；空位全部分配完毕则移出待分配堆。
func (e *Engine) assignSlotLocked(c *caseFile, idx int, reviewerID string) {
	s := c.slots[idx]
	s.assigned = true
	s.assignee = reviewerID
	s.assignedAt = e.now
	for _, sl := range c.slots {
		if !sl.assigned {
			return
		}
	}
	e.dequeueLocked(c.id)
}

// firstOpenSlot 返回最小的未分配空位下标，无则返回 -1。
func firstOpenSlot(c *caseFile) int {
	for i, s := range c.slots {
		if !s.assigned {
			return i
		}
	}
	return -1
}

// eligibility 校验双人复核与仲裁空位的级别及同人约束：
// 第二名复核员必须为二级且与第一名不同人；
// 仲裁员须为二级、不同于前两人（利益冲突在调用方先行校验）。
func eligibility(c *caseFile, idx int, r Reviewer) error {
	if c.kind == kindDouble && idx == 1 {
		if c.slots[0].assignee == r.ID {
			return ErrSameReviewer
		}
		if r.Level != LevelTwo {
			return ErrLevelMismatch
		}
	}
	if idx == 2 {
		if c.slots[0].assignee == r.ID || c.slots[1].assignee == r.ID {
			return ErrSameReviewer
		}
		if r.Level != LevelTwo {
			return ErrLevelMismatch
		}
	}
	return nil
}
