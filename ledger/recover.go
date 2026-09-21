package ledger

import "ontology/wal"

// CrashAt 让下一次 Transfer 在 p 处中断（返回 ErrCrashed）。
// 崩溃点是一次性的，触发后自动复位为 CrashNone。
func (g *Ledger) CrashAt(p CrashPoint) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.crash = p
}

// Recover 重放 WAL，把分片状态修到一致。
//
// 已应用过的 Txn 由 shard.Set 按每片 LastTxn 幂等跳过，因此重复调用
// Recover 结果相同，且第二次起 replayed 为 0。replayed 统计的是本次
// 真正新应用到分片上的记录条数。
//
// Recover 与 Transfer 共用同一把锁（独占式恢复）：恢复期间并发转账
// 会阻塞直到恢复结束，保证恢复不会观察到转账的中间态。
//
// 根因修复：Recover 结束后 WAL 中全部记录都已确认应用，必须推进
// appliedSeq；此前不推进，导致崩溃恢复后的记录永远无法被 Checkpoint
// 截断，appliedSeq 也与“已确认全部应用的最大 Seq”的语义不符。
func (g *Ledger) Recover() (replayed int, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	recs, err := g.log.Replay(0)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		if g.set.Apply(r) {
			replayed++
		}
	}
	g.appliedSeq = g.log.LastSeq()
	return replayed, nil
}

// Checkpoint 把 WAL 截断到“所有分片都已应用”的最大 Seq。
// 故意不直接取 LastSeq：若上一笔转账在 Apply 前崩溃，其记录尚未应用，
// 必须留在 WAL 里等 Recover 重放，否则截断后崩溃将永久丢失该 Txn。
//
// 根因修复：Truncate 的语义是“丢弃 Seq <= upto”，此前误传
// appliedSeq+1，把第一条未应用的记录也截掉，崩溃恢复因此永久丢账。
func (g *Ledger) Checkpoint() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.log.Truncate(g.appliedSeq)
}

// AppliedSeq 返回当前已确认全部应用的最大 Seq，供测试与调试使用。
func (g *Ledger) AppliedSeq() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.appliedSeq
}

// Replay 暴露 WAL 重放，供 demo 与测试检查日志内容。
func (g *Ledger) Replay(from uint64) ([]wal.Record, error) {
	return g.log.Replay(from)
}
