package claims

// Submit 由被分配的复核员本人提交结论。
// 拒绝次序：参数非法 > 案件不存在 > 复核员不存在 > 已终态 > 已超时 > 非本人。
// 恰在到期时刻提交的人工结论被拒绝并报“已超时”，空位按自动通过处理。
func (e *Engine) Submit(caseID string, slotIdx int, reviewerID string, concl Conclusion) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" || reviewerID == "" || slotIdx < 0 || !concl.valid() {
		return ErrInvalidParam
	}
	c, ok := e.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if _, ok := e.reviewers[reviewerID]; !ok {
		return ErrReviewerNotFound
	}
	if c.final() {
		return ErrCaseFinal
	}
	if slotIdx >= len(c.slots) {
		return ErrInvalidParam
	}
	s := c.slots[slotIdx]
	switch {
	case s.assigned && s.conclusion == ConclusionNone && s.expired(e.now, e.cfg.ReviewTimeout):
		// 目标空位已超时：结算该案件全部到期空位（含本空位自动通过）。
		e.sweepCaseLocked(c)
		return ErrSlotTimeout
	case s.conclusion != ConclusionNone && s.auto:
		// 空位结论已被超时自动填入，视同该空位复核员已提交通过。
		return ErrSlotTimeout
	case s.conclusion != ConclusionNone && s.assignee != reviewerID:
		return ErrNotAssignee
	case s.conclusion != ConclusionNone:
		return ErrSlotClosed
	case !s.assigned || s.assignee != reviewerID:
		return ErrNotAssignee
	}
	// 先结算同案件其他已到期空位，再记录本人结论，保证
	// “一人超时视为通过、另一人拒付”同样进入仲裁。
	e.sweepCaseLocked(c)
	e.recordConclusionLocked(c, slotIdx, concl, false)
	return nil
}
