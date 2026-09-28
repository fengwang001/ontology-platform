// Package txreassemble 将交错到达的变更事务事件按事务边界重组，
// 使下游以事务为单位原子地收到数据。
//
// # 事务生命周期
//
// 每个事务经历 Begin → Write* → Commit | Rollback：
//
//   - Begin(txID)：事务进入“进行中”集合。
//   - Write(txID, row)：行严格按到达顺序追加到该事务自己的缓冲区。
//   - Commit(txID)：立即整体输出该事务的完整行集（空事务也输出），
//     事务离开进行中集合，缓冲容量释放。
//   - Rollback(txID)：丢弃该事务的全部缓冲行，不产生任何输出，
//     事务离开进行中集合，缓冲容量释放。
//
// 多个事务可以交错 Begin/Write；它们的行分别缓冲、互不混杂。
//
// # 输出语义
//
// 提交是唯一的输出时刻：Commit 返回的 [CommittedTx] 即该事务的整体输出，
// 同时追加到 [Reassembler.Output] 序列。输出顺序严格等于提交的到达顺序，
// CommitSeq 从 1 起单调编号；行顺序等于该事务内行的到达顺序。
// 回滚事务的任何行都不会出现在输出中。同一输入序列反复计算得到完全相同的输出。
//
// # 缓冲上限
//
// 所有进行中事务缓冲的总行数受 New 的 maxBufferedRows 参数约束，
// 超限的 Write 以 [RejectBufferFull] 被拒绝且不写入任何内容。
// 上限按全部进行中事务的总行数计，而非按单事务计。
//
// # 拒绝原因
//
// 非法事件（空事务 ID、空行、未知事件类型）、重复 Begin、
// 对不在进行中的事务 Write/Commit/Rollback、缓冲超限，
// 分别返回 [RejectError] 且 Kind 可取 [RejectInvalidEvent]、
// [RejectDuplicateBegin]、[RejectTxNotActive]、[RejectBufferFull]。
// 被拒绝的操作不改变事务状态、缓冲或已输出序列。
//
// # 并发
//
// 所有方法均为并发安全；Commit 与 Rollback 可被并发调用。
// 可通过 [Reassembler.WithLogger] 挂接日志器，日志在决策完成、锁释放后打印，
// 内容包含输入事件、判定依据（accept / reject:<reason>）与输出结果。
package txreassemble
