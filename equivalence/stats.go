package equivalence

// ResetHopCount 将内部父指针跳数计数器清零。仅用于测试与演示。
func (u *UnionFind) ResetHopCount() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hopCount = 0
}

// HopCount 返回自上次 ResetHopCount 以来，所有 Find（含 Union/Equivalent/
// Classes 内部触发的根查找）沿父指针上行走过的跳数总和。
// 仅用于测试与演示。
func (u *UnionFind) HopCount() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hopCount
}
