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

## 增量协作式再均衡（`rebalance` 包）

消费组分区再均衡采用两轮协议，保证无需迁移的分区不中断消费，且结果确定可复现。

### 分区状态

- `Consuming`（消费中）：分区有唯一持有者，正在消费。
- `Revoking`（撤销中）：目标已改变，等待当前持有者确认撤销；持有者在确认前仍可消费。
- `Unowned`（无主）：撤销完成，等待第二轮分配。

### 两轮协议

1. **第一轮（撤销）**：成员加入/离开时，目标改变的消费中分区进入撤销中；已撤销中的分区不因目标再次变化而取消撤销。目标未变的消费中分区保持不中断。
2. **确认**：成员调用 `Confirm` 确认其全部撤销中分区，这些分区变为无主。
3. **第二轮（分配）**：当全组不存在撤销中分区时，把所有无主分区分配给当前目标并置为消费中；成员为空则不分配。

### 目标计算

对分区 `p`，在升序成员中取不小于 `p` 的最小成员；若不存在则环形回绕取最小成员（`NaiveTarget`）。成员与分区号均为非负整数。静止状态下每个分区的持有者必然等于朴素目标，可用 `NaiveTarget` 校验。

### 边界与错误类别

- 离开成员持有的分区立即变为无主（持有者已不存在，无人可确认其撤销），并在第二轮条件满足时重分配。
- 成员为空时不进行分配，分区保持无主。
- 所有非法输入整体拒绝，不产生任何成员或分区状态变更（失败不留痕），错误可用 `errors.Is` 区分：
  - `ErrInvalidArgument`：非法参数（负数成员/分区号、空或重复的分区列表）。
  - `ErrDuplicateMember`：重复加入。
  - `ErrMemberNotFound`：成员不存在（离开或确认时）。
  - `ErrNoPendingRevocation`：该成员无待确认撤销。

### 并发与确定性

`Assignor` 的所有方法可并发调用（内部互斥串行化），任意时刻每个分区至多一个持有者。成员与分区均按升序处理，相同操作序列产生完全相同的结果。

### 本地验证

```bash
# 协议与并发测试（含竞态检测，日志打印每步输入、分区状态与判定依据）
go test -race -v ./rebalance/
```
