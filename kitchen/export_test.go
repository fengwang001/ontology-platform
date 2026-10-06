package kitchen

// HypotheticalWait 仅用于测试：返回 now 时刻下一笔假想即时单的预计等待。
func (k *Kitchen) HypotheticalWait(now int64) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pump(now)
	return k.hypotheticalWait(now)
}

// WaitingIDs 仅用于测试：返回当前等待队列订单 ID（接单次序）。
func (k *Kitchen) WaitingIDs() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, len(k.waiting))
	for _, o := range k.waiting {
		out = append(out, o.info.ID)
	}
	return out
}
