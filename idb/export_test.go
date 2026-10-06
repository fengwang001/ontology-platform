package idb

// ActiveTxCountForTest 暴露调度器当前未结束事务数量，
// 用于结构性证明准入判定不随已结束事务数增长。
func ActiveTxCountForTest(k *Kernel, name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	d, ok := k.dbs[name]
	if !ok {
		return 0
	}
	return len(d.sched.order)
}
