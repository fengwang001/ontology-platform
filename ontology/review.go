package ontology

import "fmt"

func firstFreeSlot(c *caseRecord) int {
	for i, s := range c.slots {
		if !s.assigned {
			return i
		}
	}
	return -1
}

// slotEligibility 校验空位的级别与同人约束，违规划归「参数非法」：
// 双人复核第二名必须为二级且与第一名不同人；仲裁员必须为二级、
// 不同于前两人。
func slotEligibility(c *caseRecord, idx int, r *reviewer) error {
	switch idx {
	case 1:
		if r.level != LevelTwo {
			return fmt.Errorf("%w: 双人复核第二名须为二级", ErrInvalidParam)
		}
		if c.slots[0].reviewerID == r.id {
			return fmt.Errorf("%w: 第二名与第一名同人", ErrInvalidParam)
		}
	case 2:
		if r.level != LevelTwo {
			return fmt.Errorf("%w: 仲裁员须为二级", ErrInvalidParam)
		}
		if c.slots[0].reviewerID == r.id || c.slots[1].reviewerID == r.id {
			return fmt.Errorf("%w: 仲裁员与前两人同人", ErrInvalidParam)
		}
	}
	return nil
}

// occupy 把空位 idx 分配给复核员，若案件已无剩余空位则转入复核中并出堆。
func (e *Engine) occupy(c *caseRecord, idx int, reviewerID string) {
	s := c.slots[idx]
	s.assigned = true
	s.reviewerID = reviewerID
	s.assignTime = e.now
	if firstFreeSlot(c) == -1 {
		c.state = StateInReview
		e.queue.remove(c)
	}
}

// Assign 把案件的指定空位分配给指定复核员，空位须按序分配。
func (e *Engine) Assign(caseID string, slotIdx int, reviewerID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" || reviewerID == "" || slotIdx < 0 || slotIdx > 2 {
		return fmt.Errorf("%w: 编号为空或空位越界", ErrInvalidParam)
	}
	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	r, ok := e.reviewers[reviewerID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReviewerNotFound, reviewerID)
	}
	e.materialize(c)
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	if slotIdx >= len(c.slots) {
		return fmt.Errorf("%w: 案件无空位 %d", ErrInvalidParam, slotIdx)
	}
	if err := slotEligibility(c, slotIdx, r); err != nil {
		return err
	}
	if r.branch == c.branch {
		return fmt.Errorf("%w: 复核员 %q 与案件同网点 %q", ErrConflict, reviewerID, r.branch)
	}
	if c.slots[slotIdx].assigned {
		return fmt.Errorf("%w: 空位 %d", ErrSlotTaken, slotIdx)
	}
	if slotIdx != firstFreeSlot(c) {
		return fmt.Errorf("%w: 空位须按序分配", ErrInvalidParam)
	}
	e.occupy(c, slotIdx, reviewerID)
	return nil
}

// ApplyAssign 申请分配：取受理时刻最早的未分配案件（受理时刻相同按
// 受理先后），把其第一个空位分配给申请人，返回案件号与空位序号。
func (e *Engine) ApplyAssign(reviewerID string) (string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if reviewerID == "" {
		return "", 0, fmt.Errorf("%w: 复核员编号为空", ErrInvalidParam)
	}
	r, ok := e.reviewers[reviewerID]
	if !ok {
		return "", 0, fmt.Errorf("%w: %q", ErrReviewerNotFound, reviewerID)
	}
	c := e.queue.peek()
	if c == nil {
		return "", 0, fmt.Errorf("%w", ErrNoPending)
	}
	idx := firstFreeSlot(c)
	if err := slotEligibility(c, idx, r); err != nil {
		return "", 0, err
	}
	if r.branch == c.branch {
		return "", 0, fmt.Errorf("%w: 复核员 %q 与案件同网点 %q", ErrConflict, reviewerID, r.branch)
	}
	e.occupy(c, idx, reviewerID)
	return c.id, idx, nil
}

// Submit 由被分配的复核员本人提交结论（通过或拒付）。
// 本人空位已因超时被自动填入的，报「已超时」；案件进入终态后
// 其他人再提交的，报「已终态」。
func (e *Engine) Submit(caseID, reviewerID string, v Verdict) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" || reviewerID == "" || (v != VerdictPass && v != VerdictReject) {
		return fmt.Errorf("%w: 编号为空或结论非法", ErrInvalidParam)
	}
	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	if _, ok := e.reviewers[reviewerID]; !ok {
		return fmt.Errorf("%w: %q", ErrReviewerNotFound, reviewerID)
	}
	e.materialize(c)
	for _, s := range c.slots {
		if s.assigned && !s.done && s.reviewerID == reviewerID {
			s.done = true
			s.verdict = v
			e.resolve(c)
			return nil
		}
	}
	for _, s := range c.slots {
		if s.assigned && s.auto && s.reviewerID == reviewerID {
			return fmt.Errorf("%w: 空位已按通过自动填入", ErrTimeout)
		}
	}
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	return fmt.Errorf("%w: 复核员 %q 在该案无待提交空位", ErrNotAssignee, reviewerID)
}

// Withdraw 撤回未进入终态的案件；撤回为终态，与通过、拒付可区分，
// 尚未提交的复核空位作废。
func (e *Engine) Withdraw(caseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" {
		return fmt.Errorf("%w: 案件号为空", ErrInvalidParam)
	}
	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	e.materialize(c)
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	c.state = StateWithdrawn
	e.queue.remove(c)
	for _, s := range c.slots {
		if s.assigned && !s.done {
			s.done = true
			s.void = true
		}
	}
	return nil
}
