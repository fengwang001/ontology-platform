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

## 消费组协调者（`group` 包）

`group.Coordinator` 实现消费组的加入与同步状态机。时钟由调用方以毫秒
传入（`now`），再均衡超时为 `T` 毫秒，初始代数为 0。所有方法可并发
调用，结果等价于某个串行顺序；相同的调用序列重放得到完全相同的
状态序列、领导者与代数。

### 四状态迁移表

| 当前状态 | 触发 | 下一状态 | 说明 |
| --- | --- | --- | --- |
| 空 | Join | 准备中 | 记开始时刻为 now；通常同次调用内立即完成 |
| 稳定 / 等待分配 | Join | 准备中 | 新成员加入或已知成员再次加入，开始时刻为 now |
| 稳定 / 等待分配 | Leave | 准备中 | 开始时刻为 now；成员清空则进入空状态 |
| 准备中 | Join | 准备中 / 等待分配 | 首次加入记入本轮加入序；全员到齐立即完成 |
| 准备中 | Leave | 准备中 / 等待分配 / 空 | 剩余成员齐备立即完成；本轮无任何加入则清空 |
| 准备中 | Tick（now ≥ 开始+T） | 等待分配 / 空 | 超时剔除未加入者；本轮无加入则清空 |
| 等待分配 | Sync（领导者提交合法分配表） | 稳定 | 分配表成员集须恰等于本代成员集 |
| 任意 | 成员清空 | 空 | 代数不变 |

### 完成条件（准备中 → 等待分配）

满足以下任一条件即完成，完成时代数加一：

- 所有已知成员（含上一代存活成员）都已在本轮加入，在触发它的那一次
  调用内立即完成；
- `Tick(now)` 推进到 `now >= 开始时刻 + T`，未加入的已知成员被移除。

若超时或离开后本轮没有任何成员已加入，则成员全部移除、组进入空状态
且代数不变（无法选出领导者，不能完成）。

### 领导者选取

完成时领导者为本轮加入序最早的成员（而非上一代领导者）。准备中重复
加入幂等，不改变成员在加入序中的位置。

### 拒绝原因

所有拒绝均可通过 `errors.Is` 区分：`ErrClockBackwards`（时钟倒退）、
`ErrUnknownMember`、`ErrStaleGeneration`、`ErrRejoinNeeded`、
`ErrNotReady`、`ErrNotLeader`、`ErrAssignmentMismatch`。被拒绝的操作
不改变状态、代数、加入序与开始时刻。

### 本地验证

```bash
# 单元测试 + 朴素模拟对照 + 并发（含竞态检测）
go test -race -v ./group

# 模拟对照日志会逐步打印输入、输出与判定依据
go test -run TestScriptedSequenceAgainstSim -v ./group
```
