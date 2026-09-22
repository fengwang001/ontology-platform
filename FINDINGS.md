# FINDINGS：租约管理器测试补齐报告

本次只新增测试与本文档，`internal/lease/` 的实现（含注释）与
`cmd/demo/main.go` 的既有内容均未改动（demo 仅在末尾追加一段边界演练）。

## 一、钉住的实现取舍（注释原话 -> 测试）

1. **到期那一刻即失效（左闭右开区间）**
   - 原话（errors.go ErrLeaseExpired）："租约在 now >= expiresAt 时失效，
     即到期那一刻起续约与写入都算失效"；（manager.go）"租约有效当且仅当
     held && now() < expiresAt（左闭右开区间）"。
   - 测试：`TestBoundaryWriteAtExpiry`、`TestBoundaryRenewAtExpiry`、
     `TestBoundaryWriteValidBeforeExpiry`、`TestBoundaryOtherAcquiresAtExpiry`、
     `TestExpiryInvalidatesImmediately`。
   - 结论：到期那一刻，Renew / Write / 有效性判定全部落在"失效"一侧；
     同一时刻新持有者可以立刻 Acquire（互斥无空窗、无重叠）。

2. **三类哨兵错误两两可区分**
   - 原话（manager.go Renew）："holder 不是当前记录的持有者（被抢占/从未
     持有/已释放）：ErrNotHolder；holder 匹配但 token 不符：ErrTokenMismatch；
     holder 与 token 都匹配但租约已过期：ErrLeaseExpired"；
     （errors.go ErrNotHolder）"与'自己持有但已过期'返回的 ErrLeaseExpired
     是不同类别"。
   - 测试：`TestRenewThreeSentinelsDistinct`（三种场景各自 errors.Is 判定
     一个哨兵、判否另外两个）、`TestReleaseSentinelsDistinct`。
   - 结论：是三个不同的哨兵错误，不是同一个；被抢占者续约拿到 ErrNotHolder。

3. **过期但 holder/token 匹配的 Release 算成功（幂等清理）**
   - 原话（manager.go Release）："holder 与 token 都匹配当前记录时，即使
     租约已经过期，Release 也算成功（幂等清理）……但 holder 不匹配仍返回
     ErrNotHolder，token 不符仍返回 ErrTokenMismatch"。
   - 测试：`TestReleaseExpiredLeaseSucceeds`、`TestReleaseFailedKeepsLease`。
   - 结论：成功；且清理后记录被清除（再 Release/Renew 得 ErrNotHolder），
     token 计数器不复位。

4. **同 holder 有效期内重复 Acquire = 重新获取（新 token + 刷新到期）**
   - 原话（manager.go Acquire）："视为'重新获取'——签发一个全新 token 并
     刷新到期时刻，而不是报错……旧 token 立即失效"。
   - 测试：`TestSameHolderReAcquireIssuesNewToken`、
     `TestSameHolderReAcquireRefreshesExpiry`。
   - 结论：发新 token（旧值 +1），旧 token 立即被 Write 拒绝
     （ErrStaleToken），到期时刻从重新获取的时刻重算。

5. **"前一个 token"与"从未发出的巨大 token"同类**
   - 原话（errors.go ErrStaleToken）："token 是'当前 token 的前一个值'与
     '从未发出过的巨大值'归为同一类错误 ErrStaleToken"。
   - 测试：`TestWriteStaleTokenSameClass`（两者是同一个哨兵，且与其余
     三类哨兵全部判否）。

6. **Release 不复位 token 计数器**
   - 原话（manager.go）："即使租约被释放，计数器也不复位（不变量 2）"。
   - 测试：`TestTokenMonotonicAcrossRelease`、`TestReleaseExpiredLeaseSucceeds`、
     `TestConcurrentChaos`（token 序列恰好 1..n 无缺口）。

## 二、改坏方式 -> 失败测试（判别力对应表）

| # | 把实现改成 | 会失败的测试 |
|---|-----------|-------------|
| 1 | `validLocked`/Write/Renew 的边界判定由 `<`/`>=` 改成 `<=`/`>`（到期那一刻算有效） | `TestBoundaryWriteAtExpiry`、`TestBoundaryRenewAtExpiry`、`TestBoundaryOtherAcquiresAtExpiry` |
| 2 | Renew 中 `ErrLeaseExpired` 换成 `ErrNotHolder`（或三哨兵合并为一个） | `TestRenewThreeSentinelsDistinct` |
| 3 | Renew 中 token 检查挪到 holder 检查之前（被抢占者报 ErrTokenMismatch） | `TestRenewThreeSentinelsDistinct`（"被抢占者续约"用例） |
| 4 | Write 把"巨大未知 token"与"旧 token"拆成两种错误 | `TestWriteStaleTokenSameClass` |
| 5 | Release 增加"过期即返回 ErrLeaseExpired"的提前返回 | `TestReleaseExpiredLeaseSucceeds` |
| 6 | Acquire 对同人重复获取改为返回 ErrLeaseHeld | `TestSameHolderReAcquireIssuesNewToken` |
| 7 | Acquire 同人重复获取时复用旧 token（删掉 `token++`） | `TestSameHolderReAcquireIssuesNewToken`、`TestTokenMonotonicAcrossRelease` |
| 8 | Release 成功路径里加 `m.token = 0`（复位计数器） | `TestTokenMonotonicAcrossRelease`、`TestReleaseExpiredLeaseSucceeds` |
| 9 | Write 把 `m.store[key] = val` 挪到校验之前（先写后校验） | `TestRejectedWriteHasNoSideEffect`、`TestConcurrentChaos` |
| 10 | Write 删掉过期检查分支 | `TestExpiryInvalidatesImmediately`、`TestBoundaryWriteAtExpiry` |
| 11 | Acquire 删掉 ErrLeaseHeld 检查（允许抢占有效租约） | `TestMutualExclusion`、`TestSmokeSecondHolderRejected` |
| 12 | Manager 方法删掉 `mu.Lock`（失去互斥） | `TestConcurrentChaos`（-race 报警 + token 序列断言） |

## 三、与五条不变量的对照与观察

- 五条不变量均有直接测试：互斥 `TestMutualExclusion`；token 单调
  `TestTokenMonotonicAcrossRelease`；fencing `TestFencingRejectsStaleHolders`；
  无副作用 `TestRejectedWriteHasNoSideEffect`；过期即失效
  `TestExpiryInvalidatesImmediately`；并发下不变量 2+4 `TestConcurrentChaos`。
- **未发现取舍与不变量冲突。** 两条值得记录的观察（均不改实现）：
  1. "过期但匹配的 Release 算成功"与不变量 5 不冲突：Release 是清理路径，
     不改变"过期后写/续约已失效"的事实；但它使同一租约在过期后、Release
     前后的 Renew 错误从 ErrLeaseExpired 变为 ErrNotHolder——这是实现注释
     明确选择的行为，已由 `TestReleaseExpiredLeaseSucceeds` 钉住。
  2. "同人重复 Acquire 刷新到期时刻"意味着 holder 可以通过重复 Acquire
     代替 Renew 来续期（且不需要旧 token）。这不违反任何不变量（互斥的
     持有者没变、token 仍严格递增），但客户端不应依赖旧 token 在重复
     Acquire 后仍然有效，已由 `TestSameHolderReAcquireIssuesNewToken` 钉住。

## 四、验证结果

- `gofmt -l .`：无输出
- `go vet ./...`：干净
- `go test -count=1 ./...`：全过
- `go test -race -count=1 ./...`：全过
- `go run ./cmd/demo`：全部 OK，退出码 0（追加段输出 3 行）
