package remittance

// GetTransfer 查询单笔汇款在 now 时刻的状态。
//
// 查询是被接受的操作：参数校验与时钟检查通过后，会把该汇款人
// deadline < now 的待审核汇款物化为逾期失败。因此“任意时刻查询到的
// 状态与占用”只取决于此前已被接受的操作与本次 now，可精确复现。
func (e *Engine) GetTransfer(transferID int64, now int64) (TransferInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if transferID <= 0 {
		return TransferInfo{}, newError(ErrCodeInvalidArgument,
			"get: transfer id must be positive, got %d", transferID)
	}
	if now < 0 {
		return TransferInfo{}, newError(ErrCodeInvalidArgument,
			"get: now must be non-negative, got %d", now)
	}
	if err := e.checkClock(now); err != nil {
		return TransferInfo{}, err
	}
	t := e.transfers[transferID]
	if t == nil {
		return TransferInfo{}, newError(ErrCodeTransferNotFound,
			"get: transfer %d not found", transferID)
	}
	acc := e.accounts[t.sender]
	e.materializeSender(acc, now)
	acc.advance(dayIndex(now))
	e.lastNow = now
	return t.snapshot(), nil
}

// Usage 查询汇款人在 now 时刻的自然日占用与滚动年度占用。
// 只统计仍生效的占用（待审核 + 已出款；已失败/撤回/逾期已释放）。
func (e *Engine) Usage(sender string, now int64) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if sender == "" {
		return Usage{}, newError(ErrCodeInvalidArgument, "usage: empty sender")
	}
	if now < 0 {
		return Usage{}, newError(ErrCodeInvalidArgument,
			"usage: now must be non-negative, got %d", now)
	}
	if err := e.checkClock(now); err != nil {
		return Usage{}, err
	}
	acc, ok := e.accounts[sender]
	if !ok {
		return Usage{}, newError(ErrCodeInvalidArgument,
			"usage: unknown sender %q", sender)
	}
	e.materializeSender(acc, now)
	day := dayIndex(now)
	acc.advance(day)
	e.lastNow = now
	return Usage{
		Day:        day,
		DayUsed:    acc.buckets[day],
		AnnualUsed: acc.annualUsed,
	}, nil
}
