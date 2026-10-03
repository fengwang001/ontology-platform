package tcc

// MaterializeExpiry 落实所有 now 时刻已到期的 Tried 记录
// （解冻并转 Cancelled(Expired)）。供协调者在 Execute/Recover
// 开始处统一调用，使后续每个分支的 Confirm/Cancel 判定互不干扰。
func (t *TCC) MaterializeExpiry(now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkClock(now); err != nil {
		return err
	}
	t.applyExpired(now)
	t.clock = now
	return nil
}
