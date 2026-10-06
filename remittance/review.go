package remittance

// reviewOrder 校验审核/撤回类操作，返回目标汇款（不做任何物化）。
//
// 优先级：参数非法 > 时钟回退 > 汇款不存在 > 当前状态不允许（含已逾期失败）。
// 逾期判断是纯时间比较（deadline < now），因此拒绝路径无需物化、
// 不改变任何状态或时钟；物化只在操作确定被接受后由调用方执行。
func (e *Engine) reviewOrder(transferID, now int64) (*account, *transfer, error) {
	if transferID <= 0 {
		return nil, nil, newError(ErrCodeInvalidArgument,
			"review: transfer id must be positive, got %d", transferID)
	}
	if now < 0 {
		return nil, nil, newError(ErrCodeInvalidArgument,
			"review: now must be non-negative, got %d", now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, nil, err
	}
	t := e.transfers[transferID]
	if t == nil {
		return nil, nil, newError(ErrCodeTransferNotFound,
			"review: transfer %d not found", transferID)
	}
	acc := e.accounts[t.sender]
	if t.status != StatusPending {
		return nil, nil, newError(ErrCodeIllegalState,
			"review: transfer %d is %s, not pending", transferID, t.status)
	}
	if t.reviewDeadline < now {
		return nil, nil, newError(ErrCodeIllegalState,
			"review: transfer %d review deadline %d passed at now=%d",
			transferID, t.reviewDeadline, now)
	}
	return acc, t, nil
}

// Approve 在提交起 R 秒内（含第 R 秒）批准汇款，成功出款，占用保留。
// 晚于第 R 秒（now > deadline）已按逾期失败处理，批准返回状态不允许。
func (e *Engine) Approve(transferID int64, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	acc, t, err := e.reviewOrder(transferID, now)
	if err != nil {
		return err
	}
	// 操作被接受：先物化本账户其他更早逾期的待审核汇款，再批准本笔。
	e.materializeSender(acc, now)
	acc.advance(dayIndex(now))
	acc.removePending(t)
	t.status = StatusSucceeded
	t.decidedAt = now
	e.lastNow = now
	return nil
}

// Reject 人工拒绝待审核汇款：汇款失败并从原占用日释放全部占用。
func (e *Engine) Reject(transferID int64, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	acc, t, err := e.reviewOrder(transferID, now)
	if err != nil {
		return err
	}
	e.materializeSender(acc, now)
	acc.advance(dayIndex(now))
	acc.removePending(t)
	t.status = StatusFailed
	t.decidedAt = now
	acc.release(t.day, t.occupied)
	e.lastNow = now
	return nil
}

// Withdraw 汇款人撤回待审核汇款，语义与拒绝完全一致。
func (e *Engine) Withdraw(transferID int64, now int64) error {
	return e.Reject(transferID, now)
}
