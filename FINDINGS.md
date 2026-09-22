# 租约管理器：测试钉住的设计取舍与判别力审计

本次只新增测试，未修改 `internal/lease/` 下任何实现文件，也未修改
`cmd/demo/main.go` 的现有内容（仅按要求在 `main` 现有语句之后追加）。

## 一、测试钉住的设计取舍（实现注释原话 → 测试函数）

| # | 实现注释原话（摘抄） | 钉住它的测试 |
|---|---|---|
| 1 | "租约有效当且仅当 held && now() < expiresAt（左闭右开区间）" | `TestOtherHolderCanAcquireExactlyAtExpiry`、`TestWriteSucceedsOneMsBeforeExpiry`、`TestWriteRejectedExactlyAtExpiry`、`TestRenewSucceedsOneMsBeforeExpiry`、`TestRenewRejectedExactlyAtExpiry` |
| 2 | "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效……若边界算有效，则同一时刻可能出现两个持有者都认为自己有效，违反互斥。" | `TestWriteRejectedExactlyAtExpiry`、`TestRenewRejectedExactlyAtExpiry` |
| 3 | "即使租约被释放，计数器也不复位（不变量 2）。" | `TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry`、`TestReleasedRecordIsClearedAndTokenCounterKept` |
| 4 | "同一 holder 在自己租约仍有效时再次 Acquire，视为"重新获取"——签发一个全新 token 并刷新到期时刻，而不是报错……旧 token 立即失效，fencing 语义不被破坏。" | `TestSameHolderReacquireIssuesNewTokenAndOldDies` |
| 5 | "被抢占后调用 Renew/Release 返回 ErrNotHolder，与"自己持有但已过期"返回的 ErrLeaseExpired 是不同类别。" | `TestPreemptedHolderRenewGetsNotHolder`、`TestExpiredButStillHolderRenewGetsLeaseExpired`、`TestRenewSentinelsArePairwiseDistinct` |
| 6 | "holder 匹配但 token 不符：ErrTokenMismatch"（且检查顺序为 holder → token → 过期，三者两两不同） | `TestHolderWithWrongTokenRenewGetsTokenMismatch`、`TestRenewSentinelsArePairwiseDistinct` |
| 7 | "holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算成功（幂等清理）……但 holder 不匹配（已被他人抢占）仍返回 ErrNotHolder，token 不符仍返回 ErrTokenMismatch，防止误清他人的租约记录。" | `TestReleaseExpiredMatchingLeaseSucceeds`、`TestReleaseByPreemptedHolderGetsNotHolder`、`TestReleaseWithWrongTokenGetsTokenMismatchAndKeepsLease` |
| 8 | "token 是"当前 token 的前一个值"与"从未发出过的巨大值"归为同一类错误 ErrStaleToken。" | `TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel` |
| 9 | "过期后记录仍保留，用于把 ErrLeaseExpired 与 ErrNotHolder 区分开；只有 Release 成功或他人 Acquire 才会清除/覆盖该记录。" | `TestValidityFlipsAtBoundaryAndExpiredRecordBlocksUntilAcquire`、`TestReleasedRecordIsClearedAndTokenCounterKept` |
| 10 | store.go："token 匹配但租约已过期（含恰好到期的边界时刻）：ErrLeaseExpired"（区别于 token 不符的 ErrStaleToken） | `TestExpiredTokenWriteRejectedExactlyAndAfterExpiry` |
| 11 | store.go："任何被拒绝的 Write 都不会触碰 store（不变量 4）：所有校验都在写之前完成，失败路径没有任何副作用。" | `TestRejectedWritesHaveNoSideEffectsAcrossKeys`、`TestWriteRejectedExactlyAtExpiry` |
| 12 | 参数契约："holder 为空串 → ErrInvalidHolder""ttlMillis <= 0 → ErrInvalidTTL" | `TestAcquireRejectsInvalidArgs`、`TestRenewRejectsInvalidArgs`、`TestReleaseEmptyHolderRejected`（冒烟测试 `TestSmokeInvalidArgs` 保留未动） |

## 二、五条不变量 → 测试

1. 互斥：`TestMutualExclusionSecondHolderRejectedWhileActive`、
   `TestOtherHolderCanAcquireExactlyAtExpiry`、
   `TestPreemptedHolderWriteRejectedAndCurrentHolderUnaffected`、
   `TestConcurrentPreemptRenewWriteRelease`（锁内采样，重叠计数须为 0）。
2. token 严格单调：`TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry`
   （覆盖 Release 后、过期后、同人重获等全部路径）、
   `TestConcurrentPreemptRenewWriteRelease`（收集全部已发 token，排序后严格递增无重复）。
3. fencing：`TestPreemptedHolderWriteRejectedAndCurrentHolderUnaffected`、
   `TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel`、
   `TestWriteWhenNoLeaseEverIssuedIsStale`。
4. 无副作用：`TestRejectedWritesHaveNoSideEffectsAcrossKeys`（所有拒绝路径
   跑一遍后与调用前快照逐键 diff，含"写新键"路径）、
   并发测试中用只在写成功时更新的 oracle 与最终 store 逐键比对。
5. 过期即失效：`TestWriteRejectedExactlyAtExpiry`、
   `TestRenewRejectedExactlyAtExpiry`、
   `TestExpiredTokenWriteRejectedExactlyAndAfterExpiry`。

## 三、"改坏方式 → 失败测试"对照表（均已人工实测）

下表每一行都按描述临时改坏实现、跑对应测试确认 FAIL、再恢复确认全绿。
同样的表也以注释形式放在 `internal/lease/mutation_audit_test.go`。

| # | 文件/判定 | 改坏方式 | 必然失败的测试 |
|---|---|---|---|
| 1 | `manager.go` Acquire 互斥判定 | 删掉 `now < m.expiresAt`（过期后他人仍被拒） | `TestOtherHolderCanAcquireExactlyAtExpiry` |
| 2 | 同上 | `now < m.expiresAt` 改成 `now <= m.expiresAt` | `TestOtherHolderCanAcquireExactlyAtExpiry` |
| 3 | `manager.go` Acquire `m.token++` | 同 holder 重复 Acquire 直接返回旧 token、不递增 | `TestSameHolderReacquireIssuesNewTokenAndOldDies` |
| 4 | `manager.go` Release 清记录处 | 增加 `m.token = 0` 复位计数器 | `TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry` |
| 5 | `manager.go` Renew 过期判定 | `now >= m.expiresAt` 改成 `now > m.expiresAt` | `TestRenewRejectedExactlyAtExpiry` |
| 6 | `store.go` Write 过期判定 | `now >= m.expiresAt` 改成 `now > m.expiresAt` | `TestWriteRejectedExactlyAtExpiry`、`TestExpiredTokenWriteRejectedExactlyAndAfterExpiry` |
| 7 | `store.go` Write 写 store 的位置 | `m.store[key] = val` 挪到过期校验之前 | `TestWriteRejectedExactlyAtExpiry`、`TestRejectedWritesHaveNoSideEffectsAcrossKeys` |
| 8 | `manager.go` Renew token 分支 | `ErrTokenMismatch` 换成 `ErrNotHolder` | `TestHolderWithWrongTokenRenewGetsTokenMismatch`、`TestRenewSentinelsArePairwiseDistinct` |
| 9 | `manager.go` Renew holder 分支 | `ErrNotHolder` 换成 `ErrLeaseExpired` | `TestPreemptedHolderRenewGetsNotHolder`、`TestRenewSentinelsArePairwiseDistinct` |
| 10 | `manager.go` Release token 校验 | 删掉 `token != m.token` 的提前返回 | `TestReleaseWithWrongTokenGetsTokenMismatchAndKeepsLease` |
| 11 | `manager.go` Release 清记录前 | 插入过期即 `return ErrLeaseExpired` | `TestReleaseExpiredMatchingLeaseSucceeds` |
| 12 | `store.go` Write stale 判定 | "前一个 token"单独返回另一哨兵 | `TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel` |
| 13 | `manager.go` 临界区 | 删掉 Acquire 的 `m.mu.Lock()/Unlock()` | `TestConcurrentPreemptRenewWriteRelease` 在 `-race` 下报 DATA RACE（实测可稳定复现） |

## 四、取舍与五条不变量是否冲突

未发现冲突。三条最容易被质疑的取舍复核如下：

- 同人有效期间重复 Acquire 发新 token：持有者身份未变，不违反互斥；
  token 仍严格递增，且旧 token 立即失效，fencing 不被破坏。
- 过期但 holder/token 匹配的 Release 成功：过期租约本已无效，幂等清理
  不改变任何有效状态；token 计数器不复位。
- "前一个 token"与"巨大未知 token"同归 `ErrStaleToken`：fencing 只要求
  拒绝一切非当前 token 的写，合并类别不影响安全性。

一个值得调用方注意（但非缺陷）的语义：Renew 的检查顺序是
holder → token → 过期。因此"自己持有、token 已过时、且租约也已过期"
拿到的是 `ErrTokenMismatch` 而非 `ErrLeaseExpired`；这正是实现注释声明的
顺序，已由 `TestHolderWithWrongTokenRenewGetsTokenMismatch` 等钉住。
