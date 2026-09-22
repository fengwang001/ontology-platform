# FINDINGS：租约管理器测试钉住的取舍与判别力

本次只新增测试（`internal/lease/*_test.go`）与在 `cmd/demo/main.go`
末尾追加演示，**未修改 `internal/lease/` 下任何非测试文件**。
原有 3 个冒烟测试全部保留。

## 一、被钉住的实现取舍（注释原话 → 测试函数）

| # | 实现注释原话（摘抄） | 钉住它的测试 |
|---|---|---|
| 1 | "租约有效当且仅当 held && now() < expiresAt（左闭右开区间）" | `TestBoundaryValidLockedIsHalfOpenInterval` |
| 2 | "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效" | `TestBoundaryRenewExactlyAtExpiry`、`TestBoundaryWriteExactlyAtExpiry` |
| 3 | "新持有者在 now == expiresAt 时立刻 Acquire 成功，而旧持有者在同一时刻的写必须被拒绝" | `TestBoundaryAcquireByOtherExactlyAtExpiry` |
| 4 | Renew 错误三分类："holder 不是当前记录的持有者……ErrNotHolder"、"holder 匹配但 token 不符：ErrTokenMismatch"、"holder 与 token 都匹配但租约已过期：ErrLeaseExpired" | `TestSentinelErrorsArePairwiseDistinct`、`TestSentinelNotHolderDistinctFromExpired`、`TestSentinelTokenMismatchDistinctFromNotHolder` |
| 5 | Release："holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算成功（幂等清理）" | `TestReleaseExpiredButMatchingSucceeds` |
| 6 | Release："holder 不匹配（已被他人抢占）仍返回 ErrNotHolder，token 不符仍返回 ErrTokenMismatch，防止误清他人的租约记录" | `TestReleasePreemptedAndWrongTokenRejected` |
| 7 | Acquire："同一 holder 在自己租约仍有效时再次 Acquire，视为'重新获取'——签发一个全新 token 并刷新到期时刻，而不是报错……旧 token 立即失效" | `TestReacquireSameHolderIssuesNewToken`、`TestReacquireOldTokenImmediatelyInvalid`、`TestReacquireRefreshesExpiry` |
| 8 | ErrStaleToken："token 是'当前 token 的前一个值'与'从未发出过的巨大值'归为同一类错误 ErrStaleToken" | `TestWritePreviousAndUnknownTokenSameSentinel` |
| 9 | "即使租约被释放，计数器也不复位（不变量 2）" | `TestInvariantTokensStrictlyMonotonic`、`TestReleaseExpiredButMatchingSucceeds` |

## 二、五条不变量 → 测试函数

| 不变量 | 测试 |
|---|---|
| 1 互斥 | `TestInvariantMutualExclusion`、`TestBoundaryAcquireByOtherExactlyAtExpiry`、`TestConcurrentChaosRace`（终态唯一赢家） |
| 2 token 严格单调 | `TestInvariantTokensStrictlyMonotonic`、`TestConcurrentChaosRace`（发出集合恰为 {1..K}，无重复无跳号） |
| 3 fencing | `TestInvariantFencingRejectsStaleTokens`、`TestWritePreviousAndUnknownTokenSameSentinel` |
| 4 拒绝无副作用 | `TestInvariantRejectedWriteHasNoSideEffect`、`TestInvariantExpiryInvalidatesImmediately`、`TestConcurrentChaosRace`（最终值不来自拒绝集合） |
| 5 过期即失效 | `TestInvariantExpiryInvalidatesImmediately`、`TestBoundaryRenewExactlyAtExpiry`、`TestBoundaryWriteExactlyAtExpiry` |

## 三、判别力："改坏方式 → 失败测试"对应表

每行都是对**现有实现一处判定**的具体变异，以及会因此失败的测试。
对应说明也以注释写在各测试函数旁。

| # | 文件 | 把该处判定改成 | 会失败的测试 |
|---|---|---|---|
| M1 | `manager.go` `validLocked` / `Renew`：`now >= m.expiresAt` | 改成 `now > m.expiresAt`（到期那一刻算有效） | `TestBoundaryRenewExactlyAtExpiry` |
| M2 | `store.go` `Write`：`now >= m.expiresAt` | 改成 `now > m.expiresAt` | `TestBoundaryWriteExactlyAtExpiry`、`TestInvariantExpiryInvalidatesImmediately` |
| M3 | `manager.go` `Acquire`：他人有效持有的判定 `now < m.expiresAt` | 改成 `now <= m.expiresAt`（到期那一刻仍拒绝新持有者） | `TestBoundaryAcquireByOtherExactlyAtExpiry` |
| M4 | `manager.go` `Renew`：被抢占分支返回 `ErrNotHolder` | 换成 `ErrLeaseExpired` 或 `ErrTokenMismatch` | `TestSentinelErrorsArePairwiseDistinct`、`TestSentinelNotHolderDistinctFromExpired` |
| M5 | `manager.go` `Renew`：token 不符分支返回 `ErrTokenMismatch` | 换成 `ErrNotHolder` | `TestSentinelTokenMismatchDistinctFromNotHolder`、`TestReacquireOldTokenImmediatelyInvalid` |
| M6 | `manager.go` `Acquire`：`m.token++` | 挪到 holder/TTL 参数校验**之前**（非法调用也消耗号） | `TestReacquireDoesNotConsumeTokenOnInvalidArgs` |
| M7 | `manager.go` `Acquire`：同 holder 重获仍执行 `m.token++` 覆盖当前 token | 重获时不更新 `m.token`（旧号继续可用） | `TestReacquireOldTokenImmediatelyInvalid` |
| M8 | `manager.go` `Release`：匹配后直接清理 | 在清理前插入 `if now >= expiresAt { return ErrLeaseExpired }`（过期 Release 失败） | `TestReleaseExpiredButMatchingSucceeds` |
| M9 | `store.go` `Write`：`!m.held || token != m.token` 统一返回 `ErrStaleToken` | 对"曾经合法的旧 token"特判成另一哨兵（如 `ErrTokenMismatch`） | `TestWritePreviousAndUnknownTokenSameSentinel` |
| M10 | `store.go` `Write`：先校验后写 `m.store[key] = val` | 把赋值挪到过期校验之前（拒绝路径触碰 store） | `TestInvariantRejectedWriteHasNoSideEffect`、`TestConcurrentChaosRace` |
| M11 | `manager.go` `Acquire`：他人持有时返回 `ErrLeaseHeld` | 直接放行（允许并发双持有者） | `TestInvariantMutualExclusion`、`TestConcurrentChaosRace` |

## 四、并发测试说明

`TestConcurrentChaosRace`：8 个 goroutine × 200 轮，TTL 固定 4ms，
逻辑时钟为 `atomic.Int64`，每轮只前进 1~3ms（不使用 sleep），
因此大量自然产生"到期、被抢占、旧号写、续约、释放"的交错。
结束后断言：

- 所有成功 `Acquire` 发出的 token 集合恰好是 `{1..K}`——
  重复、回退、跳号都会被抓住（不变量 2）；
- 每个键的最终值必须来自某次**成功**的写；任何只在被拒绝写中
  出现过的值若成为最终值即失败（不变量 4）；
- 全部租约过期后并发 Acquire，恰好 1 个赢家（不变量 1）。

## 五、结论：取舍与五条不变量是否冲突

未发现冲突。三条最容易被质疑的取舍均自洽：

- "到期那一刻算失效"（左闭右开）是互斥的必要条件：若边界算有效，
  新持有者在 `now == expiresAt` Acquire 成功的同时旧持有者仍可写，
  同一时刻存在两个有效持有者，直接违反不变量 1。
- "过期但 holder/token 匹配的 Release 成功"只是幂等清理，
  不恢复任何已失效的写权限，不违反不变量 3/5。
- "同 holder 有效期间重获发新 token、旧号立即失效"缩小而非扩大
  写权限集合， fencing 语义不被破坏（不变量 3 仍成立）。
