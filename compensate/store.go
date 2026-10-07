package compensate

// Store 是子系统依赖的可串行化事务型键值存储抽象。
// 生产环境可由带 SI/SSI 隔离级别的数据库（如 PostgreSQL SERIALIZABLE、
// FoundationDB、etcd 事务）实现；测试使用进程内内存实现。
//
// 约定：Txn 内的读集合参与可串行化校验；提交时若无法排在某个串行顺序中，
// 返回 ErrConflict，由调用方整体重试。事务内绝不允许部分对外可见：
// 所有 Put/Delete 只在 Commit 成功时原子生效，崩溃/返回错误时一条都不生效。
type Store interface {
	// Update 在一个可串行化事务内执行 fn；冲突时由实现负责重试，
	// 直到提交成功或 fn 返回非 ErrConflict 错误。
	Update(fn func(txn Txn) error) error
}

// Txn 是单个可串行化事务内的读写句柄。
type Txn interface {
	Get(key string) ([]byte, error)
	Put(key string, value []byte)
	Delete(key string)
}

// EffectClaimer 是补偿副作用专用的原子条件写能力：
// 仅当 key 在当前已提交存储与本事务缓冲中都不存在时才写入，返回 true；
// 否则不写入并返回 false。它在提交临界区完成「检查不存在 + 写入」的原子合并，
// 保证同一 EventID 同一下标的副作用即使被并发的重复投递/续作同时尝试，
// 也恰有一个事务真正施加（消除部分重复）。
type EffectClaimer interface {
	CreateIfAbsent(key string, value []byte) bool
}

// predicateTxn 是可选实现的事务能力：开启后，对「不存在键」的读取会登记
// 谓词锁，使并发插入该键的后提交事务中止（first-committer-wins）。
// 处理器只在领取/撤销裁决事务中使用；普通工作事务通过已存在记录的版本校验
// 达成串行化，避免不必要的插入冲突。
type predicateTxn interface {
	EnablePredicateLocksFor(prefixes ...string)
}
