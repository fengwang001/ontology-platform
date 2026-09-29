# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多阶段会签审批流引擎（`approval` 包）

`approval/` 实现线程安全的多阶段会签（countersign）审批引擎：按阶段收集同意/否决，
满足所需同意数即推进，数学上确定已不可能达到时提前驳回，每个流程恰好得到一个终局。

### 推进与提前驳回判定式

设当前阶段审批人数为 `n`、所需同意数为 `k`、已收到同意数为 `a`、已表态人数为 `v`
（有效表态，含同意与否决；委托票计入委托人）。每次有效表态后按序判定：

- 推进/通过：`a >= k`。末阶段满足则终局=**通过**；非末阶段则清空阶段状态进入下一阶段。
- 提前驳回：`a + (n - v) < k`。剩余未表态者全部同意也达不到 `k`，终局=**驳回**
  （注意：不是“见到否决就驳回”，单张否决后仍可能通过）。
- 否则继续等待表态。

终局共四种且互斥：通过、驳回、超时、撤回；终局后任何操作都被拒绝且**不再产生事件**。

### 截止边界（注入时钟）

- 表态有效性看操作携带的声称时刻 `at`：仅当 `at < deadline`（严格小于）才有效；
  **恰在截止时刻 `at == deadline` 的表态无效**，此时流程已超时，拒绝原因报“已终局”。
- 任何操作进入时先以 `at` 做一次超时检查；`AdvanceClock(t)` 在时钟到达
  `t >= deadline` 而当前阶段仍未达到 `k` 时将终局置为**超时**（批量处理按流程 ID
  排序，保证确定性）。阶段推进后会立即对新阶段用同一时刻做截止检查。

### 委托规则

- 审批人只能在**当前阶段、本人表态之前**把表态权委托给一名**不属于本阶段**的人；
  受托人表态计作委托人的一票，委托人本人不得再表态。
- 受托人不能再转委托；受托人不能是本阶段成员，也不能已是他人的受托人；
  每个委托人至多一次有效委托。
- 委托仅对当前阶段生效；同一审批人在不同阶段的成员身份、表态与委托互相独立。

### 错误优先级（只报第一个命中的原因并拒绝，状态不变）

1. 流程不存在
2. 已终局（先按操作时刻补判超时；终局后不写事件）
3. 既非当前阶段审批人也非其受托人
4. 已委托者本人表态
5. 重复表态
6. 委托给自己
7. 受托人是本阶段成员或已受托者
8. 受托人再转委托
9. 已表态后再委托
10. 已委托后重复委托

表态只走 1–5；委托依次走 1、2、3（其中受托人发起委托命中 8）、6–10。

### 并发与确定性

- 引擎以单一互斥锁串行化所有表态、委托、撤回与时钟推进；任意并发交错下每个流程
  只有一个终局，事件日志为全局追加式、单调编号（`Engine.Events()`）。
- 同一操作序列串行重放得到逐字节相同的事件内容与终局（见 `TestSerialReplayDeterminism`）；
  50 轮随机并发交错 + `-race` 校验单终局与“终局后无事件”（见 `TestConcurrentInterleavings`）。

### 快速用法

```go
e := approval.NewEngine(nil) // 注入 approval.Clock 可替换时间源
id := e.Start("owner", []approval.Stage{{
    Name: "部门会签", Approvers: []string{"alice", "bob", "carol"},
    Required: 2, Deadline: deadline,
}})
e.Vote(id, "alice", approval.VoteApprove, now)
e.Delegate(id, "bob", "dave", now) // bob 委托给非成员 dave
e.Vote(id, "dave", approval.VoteApprove, now) // 计入 bob，末阶段达成即通过
```

每条事件通过 `SetLogger` 回调打印，日志含**输入、输出与判定依据**（如
`同意=1+未表态=0=1<k=2，已不可能达到所需同意数`）。

### 本地验证

```bash
go test -race -v ./approval          # 全量用例（含输入/输出/判定依据日志）
go test -run TestSerialReplayDeterminism -v ./approval
go test -run TestConcurrentInterleavings -race -v ./approval
go test -coverprofile=coverage.out ./approval && go tool cover -html=coverage.out
gofmt -l . && go vet ./...
```

测试覆盖：三人需两票时两票否决立即驳回、一票否决仍可通过、委托与转委托拒绝、
恰在截止的表态与时钟超时、撤回与最后一票并发到达、跨阶段同一审批人、重复/越权表态、
非发起人撤回以及错误优先级各分支。

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
