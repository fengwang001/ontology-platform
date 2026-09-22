# FINDINGS

本次只补测试，未改动 `internal/lease/` 的任何非测试文件。

## 一、钉住的实现取舍（注释原话 → 测试）

1. **到期边界为左闭右开区间**（`errors.go` ErrLeaseExpired 注释）：
   > "设计决策：租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效。……采用左闭右开区间 [start, expiresAt) 可以让新持有者在 now == expiresAt 时立刻 Acquire 成功，而旧持有者在同一时刻的写必须被拒绝"

   测试：`TestWriteAtExpiryBoundaryFails`、`TestRenewAtExpiryBoundaryFails`、`TestJustBeforeExpiryStillValid`、`TestAcquireByOtherAtExpiryBoundarySucceeds`、`TestWriteAfterExpiryRejected`（`internal/lease/boundary_test.go`）。

2. **被抢占/过期/token 不符是三个不同哨兵错误**（`errors.go` ErrNotHolder 注释 + `manager.go` Renew 注释）：
   > "设计决策：被抢占后调用 Renew/Release 返回 ErrNotHolder，与'自己持有但已过期'返回的 ErrLeaseExpired 是不同类别。"

   测试：`TestRenewAfterPreemptReturnsNotHolder`、`TestRenewWrongTokenReturnsTokenMismatch`、`TestRenewAfterExpiryReturnsLeaseExpired`、`TestReleaseErrorClassesMatchRenew`、`TestSentinelErrorsAreDistinct`（`internal/lease/errors_test.go`）。

3. **过期租约的 Release 幂等成功**（`manager.go` Release 注释）：
   > "设计决策：holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算成功（幂等清理）。"

   测试：`TestReleaseExpiredLeaseSucceeds`、`TestReleaseAfterPreemptReturnsNotHolder`、`TestDoubleReleaseReturnsNotHolder`（`internal/lease/release_test.go`）。

4. **同 holder 重复 Acquire = 发新 token + 刷新到期时刻**（`manager.go` Acquire 注释）：
   > "设计决策：同一 holder 在自己租约仍有效时再次 Acquire，视为'重新获取'——签发一个全新 token 并刷新到期时刻，而不是报错。……旧 token 立即失效，fencing 语义不被破坏。"

   测试：`TestSameHolderReacquireIssuesNewToken`、`TestOldTokenStaleAfterReacquire`、`TestReacquireRefreshesExpiry`、`TestFailedAcquireDoesNotConsumeToken`（`internal/lease/reacquire_test.go`）。

5. **过时 token 与未知 token 同属 ErrStaleToken**（`errors.go` ErrStaleToken 注释）：
   > "设计决策：token 是'当前 token 的前一个值'与'从未发出过的巨大值'归为同一类错误 ErrStaleToken。"

   测试：`TestPreviousAndHugeTokenSameError`（`internal/lease/write_test.go`）。

6. **被拒绝的 Write 零副作用**（`store.go` Write 注释，不变量 4）：
   > "任何被拒绝的 Write 都不会触碰 store（不变量 4）：所有校验都在写之前完成，失败路径没有任何副作用。"

   测试：`TestRejectedWriteHasNoSideEffect`、`TestConcurrentContention`。

7. **token 计数器永不复位**（`manager.go` token 字段注释，不变量 2）：
   > "即使租约被释放，计数器也不复位（不变量 2）。"

   测试：`TestReleaseDoesNotResetTokenCounter`、`TestTokenStrictlyMonotonicAcrossCycles`、`TestConcurrentContention`。

## 二、改坏方式 → 失败测试（判别力对照表）

| # | 把实现改成 | 必然失败的测试 |
|---|-----------|---------------|
| 1 | `Renew` 中 `now >= m.expiresAt` → `now > m.expiresAt` | `TestRenewAtExpiryBoundaryFails` |
| 2 | `Write` 中 `now >= m.expiresAt` → `now > m.expiresAt` | `TestWriteAtExpiryBoundaryFails` |
| 3 | `Acquire` 中 `now < m.expiresAt` → `now <= m.expiresAt` | `TestAcquireByOtherAtExpiryBoundarySucceeds` |
| 4 | `Renew` 的 `ErrNotHolder` 换成 `ErrLeaseExpired`（或与过期分支合并、先判过期再判 holder） | `TestRenewAfterPreemptReturnsNotHolder` |
| 5 | `Renew` 的 `ErrTokenMismatch` 换成 `ErrStaleToken` | `TestRenewWrongTokenReturnsTokenMismatch` |
| 6 | `Release` 增加 `if now >= m.expiresAt { return ErrLeaseExpired }` | `TestReleaseExpiredLeaseSucceeds` |
| 7 | `Release` 增加 `m.token = 0`（复位计数器） | `TestReleaseDoesNotResetTokenCounter`、`TestTokenStrictlyMonotonicAcrossCycles` |
| 8 | `Acquire` 对同 holder 也返回 `ErrLeaseHeld`（删掉 `m.holder != holder` 条件） | `TestSameHolderReacquireIssuesNewToken` |
| 9 | `Acquire` 把 `m.token++` 挪走或改为同 holder 复用旧 token | `TestSameHolderReacquireIssuesNewToken`、`TestFailedAcquireDoesNotConsumeToken` |
| 10 | `Write` 的 `ErrStaleToken` 换成 `ErrTokenMismatch`，或把 `token != m.token` 放宽为 `token > m.token` | `TestPreviousAndHugeTokenSameError` |
| 11 | `Write` 把校验挪到 `m.store[key] = val` 之后 | `TestRejectedWriteHasNoSideEffect` |
| 12 | 摘掉 `Manager.mu`（或 Write 不加锁） | `go test -race` 报 data race；`TestConcurrentContention` 内容断言失败 |

## 三、其他观察（未改实现）

- `manager.go` 中 `validLocked` 方法定义后从未被调用（`Acquire`/`Renew`/`Write` 都内联了 `now < expiresAt` 判断）。属于死代码，不影响正确性；按任务要求未做改动。
- 未发现任何一条实现取舍与五条不变量相冲突：左闭右开区间、同 holder 重取发新 token、过期 Release 幂等成功，在注释给出的理由下均自洽。
