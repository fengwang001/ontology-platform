# FINDINGS — 限流器实现行为与文档承诺的一致性核对

核对范围：`bucket`、`policy`、`limiter` 的包注释与方法注释、`README.md`。
核对方式：为每条承诺补写「先推进注入时钟、再操作」的特征测试（见各
`*_test.go` 新增文件），比对可观测行为与文档原文。实现未做任何改动。

## 不一致项

### F1. 热更新速率会追溯改写「已流逝但尚未入账」时间的时间账（真实缺陷）

- **承诺的原文出处**：
  - `bucket/bucket.go` `SetLimit` 注释："changes capacity and rate
    **without refilling or consuming tokens**"。
  - `limiter/limiter.go` `SetQuota` 注释："The change takes effect
    immediately and **neither consumes nor refills tokens**"。
- **实际观察到的行为**：速率变更会把「上次入账点到变更时刻」之间已流逝、
  但尚未通过查询/扣减入账的时间，**全部按新速率结算**：
  - 调大速率 → 过去的时间按新（更高）速率补发，余量凭空变多（效果等同
    refill）；
  - 调小速率 / 改成 0 → 静置期间按旧速率已赚得的补充缩水或全部蒸发
    （效果等同 consume）。
- **复现步骤**（注入时钟，标准库 only）：
  1. `bucket.New(100, 1, clock)`，`TryTake(100)` 抽干；
  2. 时钟前进 10s（不查询）；
  3. `SetLimit(100, 5)`；
  4. `Balance()` → **实际输出 50**（0 + 5×10，过去 10s 按新速率补发）。
     按文档「不补充也不消耗」的承诺，调整前已赚得的应是 10（0 + 1×10），
     调整后仍应是 **10**，之后的流逝才按 5/s 计。
  - 反向：`New(100, 5)` 抽干、静置 10s、`SetLimit(100, 0)` →
    `Balance()` 实际输出 **0**，而静置期已赚得 50，承诺的输出是 50。
- **根因**：`SetLimit` 直接替换 `rate`/`capacity` 而不先执行 `refill()`；
  下一次 `refill()` 用（新的）单一速率对整个 `[last, now]` 窗口结算，
  窗口的前半段实际上还处在旧速率生效期。修复方向（本次不实施）：
  `SetLimit` 先按旧速率 `refill()` 入账，再替换速率与容量。
- **严重程度**：高。运维已用 `SetQuota` 做灰度扩缩容：扩容提速会给租户
  补发其从未赚得的令牌（超发），降速/暂停会吞掉租户已赚得的令牌
  （误伤），且偏差随「距上次查询的静置时长」线性放大。
- **钉住该行为的测试**：`bucket/setlimit_rate_time_test.go`、
  `limiter/setquota_time_test.go`（断言的是当前真实行为，非期望行为）。

### F2. README 的运行与测试命令指向不存在的目标（文档错误）

- **承诺的原文出处**：`README.md`「运行」节 `go run ./cmd/server`、
  `go build -o bin/server ./cmd/server`；「测试」节 `go test ./ontology`、
  `go test -run TestObjectType ./ontology`。
- **实际观察到的行为**：仓库只有 `cmd/demo`（无 `cmd/server`），包只有
  `bucket`/`policy`/`limiter`（无 `./ontology` 包，也没有
  `TestObjectType` 用例）。上述命令全部无法执行。
- **复现步骤**：`go run ./cmd/server` → 实际输出
  `no required module provides package .../cmd/server` 类错误；文档承诺
  该命令能启动服务。
- **严重程度**：低（纯文档，不影响库行为；README 像是从上层平台模板
  复制后未随本模块更新）。

## 已核对、确认一致的条目

以下条目逐一补了特征测试或复核了既有测试，行为与文档承诺一致：

- **时钟回拨不倒扣**（`bucket.refill` 注释、"a rewind is ignored"）：
  已入账状态下回拨、回拨后推回原处、超过原位置继续补充，均符合承诺。
  依据：`bucket/rewind_pending_test.go`、既有 `TestClockRewindDoesNotDeduct`。
  备注（非不一致）：回拨发生在「未入账」状态下时，回拨区间的时间不再
  被结算（`TestRewindBeforeAnyQuerySettlesAtRewoundClock`），这是
  「回拨视为无流逝」承诺的直接推论，不算违背。
- **缩容截断 / 扩容不补满**（`SetLimit`、`SetQuota` 注释）：含「时间已
  流逝但尚未查询」的形态，可观测结果均为 `min(补充后余量, 新容量)` 与
  「不凭空补满」。依据：`bucket/setlimit_capacity_time_test.go`、
  `limiter/setquota_time_test.go`、既有 `TestSetLimitTruncatesAndNeverTopsUp`。
- **连续补充与容量封顶**（`Bucket` 注释 "refilled continuously"）：
  0.5s/1.5s/2.25s 推进产生 0.5/2.0/4.25 的连续余量，到容量后不再增长。
  依据：`bucket/refill_fractional_test.go`。
- **拒绝时两桶余量不变、错误可判定、租户优先**（`Allow` 注释、
  `errors.go` 注释）：时间流逝后仍逐一相等；`ErrTenantQuota` 在同时
  不足时优先。依据：`limiter/rollback_time_test.go`、既有
  `TestGlobalShortfallLeavesBothBalancesUntouched` 等。
- **注销不动全局桶、重建是全新满桶**（`Unregister` 注释）：旧桶的
  待补充量不随注销/重建传递。依据：`limiter/unregister_time_test.go`。
- **Inspect 纯查询**（`Inspect` 注释）：同时刻两次结果相同；推进时钟
  按速率变化；查到的余量可被 `Allow` 全额取走（查询不消耗）。依据：
  `limiter/inspect_purity_test.go`、既有 `TestInspectIsPureAndStable`。
- **并发安全与不超卖**（`Limiter` 注释 "safe for concurrent use"）：
  并发 Allow + 并发推进时钟下 `-race` 干净，放行量不超预算、令牌守恒
  （带速率租户因容量封顶的合法截断只少不多）。依据：
  `limiter/concurrent_refill_test.go`、既有 `TestConcurrentNoOversell`。
- **配额校验**（`policy.Normalize`/`Must` 注释）：非法容量/速率（含
  NaN、Inf、0、负值）的拒绝与 `Must` 的 panic 均符合承诺。依据：既有
  `policy/policy_test.go`（本包无需新增用例）。
- **退款封顶**（`Refund` 注释 "capped at capacity"）：依据既有
  `TestTryTakeAndRefund`。
