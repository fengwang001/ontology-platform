# 一致性报告：实现行为 vs 文档承诺

本次交付只补特征测试与本报告，实现保持原样。下面先列发现的不一致，
再列核对过且一致的条目。

## 发现的不一致

### F1. SetQuota/SetLimit 把"变更前已流逝但未结算的时间"按新速率、新容量重新结算

- **承诺的原文出处**：
  - `limiter/limiter.go` `SetQuota` 注释："The change takes effect
    immediately and neither consumes nor refills tokens: shrinking the
    capacity truncates the current balance, growing it does not top up."
  - `bucket/bucket.go` `SetLimit` 注释："SetLimit changes capacity and
    rate without refilling or consuming tokens. A smaller capacity
    truncates the stored balance; a larger one leaves it."
- **实际观察到的行为**：`SetLimit` 既不结算 refill 也不推进内部时间戳
  `last`，于是区间 `[last, now)`（变更前已流逝、但尚未被任何
  Balance/TryTake 结算的时间）会在下一次查询时按**新速率**补充、按
  **新容量**截断。变更并非"takes effect immediately"，而是对未结算的
 历史区间**追溯生效**。
- **复现步骤**（均可由新增测试复现）：
  1. 速率调大：`bucket.New(100, 1)` → `TryTake(100)` 抽干 → 时钟前进
     6s（不查询）→ `SetLimit(100, 10)` → `Balance()` 实际输出 **60**；
     按文档"立即生效、不补充令牌"的直觉，变更时刻余量为 6，之后未再
     流逝时间，应输出 **6**。
     （测试：`bucket/bucket_hotupdate_test.go`
     `TestSetLimitRateUpAppliesToElapsedTime`；limiter 层对应
     `TestSetQuotaRateUpAfterIdle`）
  2. 速率调小：`New(100, 10)` → 抽干 → 前进 6s → `SetLimit(100, 1)`
     → `Balance()` 实际 **6**；按旧速率已赚取 60，文档未承诺会被重算，
     应输出 **60**。
     （测试：`TestSetLimitRateDownAppliesToElapsedTime` /
     `TestSetQuotaRateDownAfterIdle`）
  3. 速率调 0：`New(100, 5)` → 抽干 → 前进 4s → `SetLimit(100, 0)` →
     `Balance()` 实际 **0**；已赚取的 20 被静默丢弃。
     （测试：`TestSetLimitRateZeroDropsPendingRefill` /
     `TestSetQuotaRateZeroAfterIdle`）
  4. 满桶长静置后扩容：`New(10, 1)`（满桶）→ 前进 100s（不查询）→
     `SetLimit(20, 1)` → `Balance()` 实际 **20**；文档承诺 "growing it
     does not top up"，变更时刻余量为 10（旧容量封顶），之后未再流逝
     时间，应输出 **10**。历史时间被按新容量重新结算，等于变相 top up。
     （测试：`TestSetLimitGrowResettlesHistoryAgainstNewCapacity` /
     `TestSetQuotaGrowAfterIdle` 情形二）
- **根因**：`bucket.SetLimit` 只更新 `capacity`/`rate` 并对**存储的
  （可能过期的）**余额做截断，没有先调用 `refill()` 把 `[last, now)`
  按旧参数结算、也没有把 `last` 推进到 `now`。因此未结算区间在下次
  `refill()` 时落入新参数。
- **严重程度**：**中**。运维正在用 `SetQuota` 做灰度扩缩容：扩容会把
  历史静置时间按新容量补进来（放量超出预期）；缩速率/调 0 会把租户已
  赚取但未查询的补充按新参数重算或清零（缩量超出预期）。行为本身
  自洽且可测试，但是否为缺陷取决于产品语义；按任务要求本次不修改
  实现，仅用测试钉住当前行为。

### F2. README 的运行/测试命令与实际工程结构不符

- **承诺的原文出处**：`README.md` "运行" 一节写 `go run ./cmd/server`、
  `go build -o bin/server ./cmd/server`；"测试" 一节写
  `go test ./ontology`、`go test -run TestObjectType ./ontology`。
- **实际观察到的行为**：仓库只有 `cmd/demo`，不存在 `cmd/server`；
  也不存在 `./ontology` 包（module 名为 `ontology`，但代码在
  `bucket`/`policy`/`limiter` 三个子包）。
- **复现步骤**：执行 `go run ./cmd/server` → 实际输出
  `no required module provides package` 类报错；文档承诺能直接运行
  服务。`go test ./ontology` 同样报包不存在。
- **严重程度**：**低**。纯文档陈旧，不影响库行为；但新人按 README
  操作会立即失败。

## 核对过且一致的条目

以下条目在补测试过程中逐一核对，实现行为与文档承诺一致，并由新增或
既有测试钉住：

- **两级扣减与回滚**：`Allow` 注释 "On rejection both balances are
  exactly as before the call" —— 全局不足时租户扣减被 `Refund` 精确
  回滚（含 refill 参与的状态），租户不足时全局桶完全不被触碰。
  （`limiter/limiter_rollback_test.go`）
- **错误可判定与租户优先**：`errors.go` "When both buckets are short,
  the tenant cause is reported first" —— 与实现一致。
  （`TestBothShortReportsTenantFirstAfterRefill`）
- **时钟不回拨**：`bucket.refill` 注释 "A clock that moves backwards is
  treated as no elapsed time" 与 `limiter.New` 注释 "a rewind is
  ignored" —— 回拨不倒扣、不重复补充，推回原处后按差额继续。
  （`bucket/bucket_refill_test.go`）
- **连续补充与容量封顶**：`bucket` 包注释 "refilled continuously at
  rate tokens per second, capped at capacity" —— 非整秒步进下补充为
  连续值，到容量后不再增长。（`TestFractionalRefillIsContinuous`）
- **注销与重建**：`Unregister` 注释 "The global bucket is untouched.
  Re-registering later starts over with a full bucket." —— 一致，旧桶
  未结算的补充与计时均不泄漏到新桶。
  （`TestUnregisterDropsPendingRefill`）
- **查询纯度**：`Inspect` 注释 "it never consumes tokens, so two calls
  at the same instant return identical results" —— 一致。
  （`TestInspectPurityAndRateChange`）
- **配额校验**：`policy.Normalize` 注释的 capacity/rate 取值约束与
  实现逐项一致（既有 `policy_test.go` 覆盖）。
- **并发安全**：`Limiter` 注释 "It is safe for concurrent use" ——
  互斥锁串行化所有路径，`go test -race` 干净，无超卖、无泄漏。
  （`limiter/limiter_concurrent_test.go`）
- **突发上限与非法数量**：`ErrExceedsBurst`/"This limiter never
  blocks waiting for tokens"、`ErrInvalidAmount`/"No tokens are
  consumed" —— 与实现一致（既有测试覆盖）。
