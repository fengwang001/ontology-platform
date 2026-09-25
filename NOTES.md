# NOTES

## 七步推导（对象 X）

| # | 操作 | 计数 | Alive | 持有者 |
|---|------|------|-------|--------|
| 1 | Create(X) | 0 | true | {} |
| 2 | Acquire(r1,X) | 1 | true | {r1} |
| 3 | Acquire(r2,X) | 2 | true | {r1,r2} |
| 4 | Acquire(r2,X) | 2 | true | {r1,r2}（幂等，不计数） |
| 5 | Release(r1,X) | 1 | true | {r2} |
| 6 | Release(r2,X) | 0 | false（归零即回收） | {} |
| 7 | Acquire(r3,X) | — | false | ErrNotFound |

- (甲) 若重复 Acquire 也 +1：第 4 步计数错成 3；第 5、6 步后停在 1，对象永不归零、永不回收（泄漏）。
- (乙) 若任一释放即回收：第 5 步 X 被错误回收（Alive=false，尽管 r2 仍持有）；第 6 步 Release(r2,X) 报 ErrNotFound。
- (丙) 第 7 步应报 ErrNotFound。若延迟回收：第 7 步成功，计数错成 1，已死对象被"复活"。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 计数一致：`reg.Registry.Acquire/Release` 按 (ref,id) 持有集合增减；`TestRefCountConsistency`（随机序列对拍）。
2. 回收时机精确：`reg.Registry.Release` 中 `refc.Counter.Dec` 归零即 delete；`TestReclaimTiming`。
3. 幂等与共享：`refc.Counter` 的 holders 集合去重；`TestIdempotentAcquire`、`TestSharedLastReleaseReclaims`。
4. 失败不留痕：各操作先校验后改状态（`reg` 中先查错再写）；`TestFailureLeavesNoTrace`。
