# AUDIT

## 第二节语义：代码位置 → 钉住它的测试

1. 不超发、`ErrTooLarge`：`sem/sem.go` Acquire 入口 `n > Cap` 检查；账本自检 `ledger/ledger.go` Check。
   测试：`TestSemantics/fast_errors`、`TestConcurrent`（每步 `Consistent` 断言 Used ≤ Cap）。
2. FIFO 不插队：`sem/sem.go` Acquire 快路径要求 `q.Len()==0`，TryAcquire 同样拒绝越队。
   测试：`TestSemantics/fifo and group wake`（队列非空时 TryAcquire 必须为 false）。
3. 成组唤醒：`sem/sem.go` drainLocked（Release 与取消路径共用，队首顺序满足到第一个不满足）。
   测试：`TestSemantics/fifo and group wake`（WakeChecked==3、WakeGranted==2）。
4. 取消零副作用、竞态不丢不重：`sem/sem.go` Acquire 取消分支锁内查 `Granted` 提交点；`waitq/waitq.go` Remove。
   测试：`TestSemantics/head cancel wakes`（推导一）、`TestFaultInjection/cancel+release`（推导二，5000 轮对齐）。
5. 非法释放：`ledger/ledger.go` Give 返回 `ErrOverRelease` 且账本不变。
   测试：`TestSemantics/fast_errors`（Release(5) 后 Used 仍为 4）。
6. 等待者上限：`sem/sem.go` Acquire 中 `q.Len() >= maxWaiters` 即返 `ErrTooManyWaiters`。
   测试：`TestSemantics/fast_errors`；故障注入一致性：`TestFaultInjection` 每轮 `Settle` 比对影子模型。

## 第四节复杂度：非导出计数器实测

- `waitq.Queue.lastCancelChecked`（waitq/waitq.go）：单次 Remove 检查节点数，100 → 1，100000 → 1（常数 ≤2）。
- `sem.Sem.wakeChecked/wakeGrant`（sem/sem.go，经 Stats 读出）：单次 Release 检查数 ≤ 满足数+1，
  100 → 100 ≤ 100+1，10000 → 10000 ≤ 10000+1，两档同界。测试：`TestComplexity`。
