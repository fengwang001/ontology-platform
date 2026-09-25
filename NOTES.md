# 写者优先读写自旋锁 NOTES

## 第三节推导：八步分步表（R=读者集合，W=写者，wW=等待写者，wR=等待读者）

| 步 | 操作 | R | W | wW | wR |
|---|---|---|---|---|---|
| 1 | T1 AcquireRead | {T1} | - | 否 | - |
| 2 | T2 AcquireRead | {T1,T2} | - | 否 | - |
| 3 | T3 AcquireWrite | {T1,T2} | - | T3 | - |
| 4 | T4 AcquireRead | {T1,T2} | - | T3 | T4 |
| 5 | T1 ReleaseRead | {T2} | - | T3 | T4 |
| 6 | T2 ReleaseRead | {} | T3 | 否 | T4 |
| 7 | T3 ReleaseWrite | {T4} | - | 否 | - |
| 8 | T4 ReleaseRead | {} | - | 否 | - |

- (甲) 第 4 步 T4 被**阻塞**（写者优先：已有写者等待，新读者不得先获读），进入 wR。若无写者优先，第 4 步后 R={T1,T2,T4}，T4 先于 T3 获读；被饿的是写者 T3——只要读者不断到来，读者集合永远非空，T3 永远等不到。
- (乙) T1、T2 持读时 T1 `TryUpgrade` 返回 `ErrUpgradeConflict`（非唯一读者，不阻塞）。若做成「持读同时自旋等写锁」：获写条件是读者集合为空，而 T1 自己持有的读永不释放，条件永假——即使 T2 释放后只剩 T1 一个读者，它也永远等不到，永久自旋死锁。
- (丙) 若第 7 步 T3 释放后忘清 wW 标志：T4 的获读条件（无写者且无等待写者）永假，T4 永远自旋。最终状态 R={}、W=-、wW=残留为真、wR={T4}，T4 被饿死。

## 第二节四条不变量：保证位置 / 钉住它的测试

1. 与朴素参照一致：`api` 内 naive 模型与真锁逐操作比对 Snapshot 四字段（api.go），`TestNaiveEquivalence`、`TestSelfCheck` 钉住。
2. 互斥性：`state.TryRead` 要求无写者、`TryWrite` 要求无写者且无读者（state/state.go），`TestEightStep` 与 `TestConcurrentMonotonic` 的 Snapshot 断言钉住。
3. 写者优先：`state.TryRead` 要求 `waitW==0`（state/state.go），`TestEightStep` 第 4 步钉住。
4. 失败不留痕：`state` 各方法先校验后变更、拒绝路径零写入（state/state.go），`TestFaultInjection` 钉住。
