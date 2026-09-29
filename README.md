# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 多阶段会签审批流引擎（`approval` 包）

流程由有序阶段组成，每阶段配置审批人集合、所需同意数 `k`（`1 <= k <= 审批人数`）
与截止时刻（由注入时钟判定，见 `approval.NewEngine(start)` 与 `Engine.AdvanceClock`）。
只有当前阶段可表态，每人每阶段至多一次有效表态。终局为 **通过 / 驳回 / 超时 / 撤回**
之一，每个流程恰好得到一个终局，终局后不再产生事件。

### 推进与提前驳回判定式

设当前阶段审批人总数为 `n`，已表态人数为 `v`（同意 `a` + 否决 `v-a`），未表态人数为 `n - v`：

- **推进**：`a >= k` 时进入下一阶段（阶段运行时状态整体重置）；末阶段满足即 **通过**。
- **提前驳回**：`a + (n - v) < k` 时立即 **驳回**——即“同意数 + 未表态人数已不可能达到 k”，
  而不是见否决即驳回（例如 3 人需 2 票时，1 票否决后仍可 2 票通过；2 票否决后才驳回）。
- **超时**：引擎时钟 `>=` 当前阶段截止且 `a < k` 时判定 **超时**（惰性判定：
  任何操作或 `AdvanceClock` 触发检查）。
- **撤回**：发起人在终局前可撤回，终局为 **撤回**。

### 委托规则

审批人可在本阶段自己表态前，把表态权委托给一名 **不属于本阶段** 的人；
受托人的表态计作委托人的一票（事件日志中记录“归属”）。委托只作用于当前阶段，
阶段推进后失效。受托人不得再转委托；已委托者本人不得再表态；已表态后不得再委托。

### 截止边界

表态有效的充要条件是表态时刻 `<` 当前阶段截止时刻。**恰在截止** 的表态无效：
时钟到达截止时流程已因未达到 `k` 而超时（终局），该表态按“已终局”被拒绝，不改变任何状态。

### 错误优先级

多个拒绝原因同时成立时，只按以下顺序报告第一个，且被拒绝的操作不改变任何状态：

1. `ErrProcessNotFound` 流程不存在
2. `ErrAlreadyTerminal` 已终局
3. `ErrNotStageMember` 既非当前阶段审批人也非其受托人
4. `ErrDelegatorVoted` 已委托者本人表态
5. `ErrDuplicateVote` 重复表态
6. `ErrDelegateToSelf` 委托给自己
7. `ErrDelegateTargetInvalid` 委托给本阶段成员或已受托者
8. `ErrTrusteeRedelegate` 受托人再转委托
9. `ErrDelegateAfterVote` 已表态后再委托

补充：`ErrAlreadyDelegated`（重复委托）、`ErrNotInitiator`（非发起人撤回）、
`ErrClockBackward`（时钟回拨）等配置/参数错误不参与上述排序。

### 并发与确定性

`Vote` / `Delegate` / `Withdraw` / `AdvanceClock` 均可并发调用：引擎内部以互斥锁串行化，
任意交错下每个流程恰好一个终局，终局后不再产生事件；同一审批人出现在多个阶段时，
表态只作用于当前阶段；同一操作序列串行重放得到完全相同的事件日志与终局
（`AdvanceClock` 按流程 ID 排序做超时判定，保证事件顺序确定）。

### 本地验证

```bash
# 全部测试（含竞态检测；日志打印每个用例的输入、输出与判定依据）
go test -race -v ./approval/

# 关键用例
go test ./approval/ -run TestTwoVetoesRejectImmediately   # 3人需2票，两票否决立即驳回
go test ./approval/ -run TestOneVetoStillPasses           # 一票否决仍可通过
go test ./approval/ -run TestDelegationAndRedelegation    # 委托与转委托
go test ./approval/ -run TestVoteExactlyAtDeadline        # 恰在截止的表态
go test ./approval/ -run TestWithdrawRacesLastVote        # 撤回与最后一票同时到达
go test ./approval/ -run TestSameApproverAcrossStages     # 跨阶段同一审批人
go test ./approval/ -run TestSerialReplayDeterminism      # 串行重放确定性
go test ./approval/ -run TestConcurrentExactlyOneTerminal # 并发下恰好一个终局
```
