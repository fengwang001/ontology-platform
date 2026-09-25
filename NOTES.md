# 增量物化视图引用计数追踪与回收 — 推导与不变量

## 七步分步表（对象 X）

| 步 | 操作 | 计数 | Alive | 持有者 |
|---|---|---|---|---|
| 1 | Create(X) | 0 | true | {} |
| 2 | Acquire(r1,X) | 1 | true | {r1} |
| 3 | Acquire(r2,X) | 2 | true | {r1,r2} |
| 4 | Acquire(r2,X) | 2 | true | {r1,r2}（幂等，不计） |
| 5 | Release(r1,X) | 1 | true | {r2} |
| 6 | Release(r2,X) | 0 | false | {}（归零即回收） |
| 7 | Acquire(r3,X) | ErrNotFound | false | {}（不可复活） |

(甲) 若重复获取也 +1：第 4 步计数错成 3；第 5 步后 2、第 6 步后 1，永远降不到 0，对象泄漏、永不回收。
(乙) 若任一释放即回收：第 5 步 X 被误回收（Alive=false，r2 的持有被丢弃）；第 6 步 Release(r2,X) 报 ErrNotFound。
(丙) 第 7 步应报 ErrNotFound。若延迟回收：第 7 步成功，计数错成 1，已回收对象被"复活"。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 计数一致=持有者集合大小：`refc.Counter` 用 set 存持有者、`Count()=len(set)`（refc/refc.go）；`TestRefCountConsistency`。
2. 归零即回收、回收即不存在：`reg.Release` 仅在 `Counter.Release` 返回 zero 时 delete（reg/reg.go）；`TestReclaimTiming`。
3. 幂等与共享：`refc.Acquire` 先查 set 命中则不加；`reg.Release` 只减持有者自己的一份；`TestIdempotentShared`。
4. 失败不留痕：`reg` 每个写操作先完整校验（空串/存在性/持有）再动状态（reg/reg.go）；`TestFailureNoSideEffect`。

另：回收判定 O(1) 由 `reg.lastChecks` 计数器证明，`TestReclaimCheckCost`；并发只读一致 `TestConcurrentReads`；`api.SelfCheck` 由 `TestSelfCheck` 钉住。
