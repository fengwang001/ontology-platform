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

## 抢占受害者选择器

核心实现位于 `preemption.go`，API 包含：

- `AddNode(name, cap)`：登记唯一节点，容量范围为 `1..10^12`。
- `SetBudget(group, allowed)`：设置预算组剩余可中断数，范围为 `0..10^9`；未设置等价于 `0`。
- `Place(pod, node)`：在容量充足时直接放置 Pod。
- `Preempt(pod)`：为高优先级 Pod 选择节点与受害者；同优先级 Pod 不可抢占。

### 候选与预算违约

对每个候选节点，优先级 `prio >= 新 Pod prio` 的 Pod 视为受保护，只参与容量占用；其余 Pod 是抢占候选。候选首先按 `prio` 降序排列，同优先级按 ID 字节序升序排列。

每个节点使用调用开始时预算的独立副本。候选按重要性序消耗所属预算组：

- 无预算组的候选不消耗预算，始终属于非违约组。
- 有预算组且扣减后 `allowed >= 0` 的候选属于非违约组。
- 预算副本已经为 `0`、继续扣减会小于 `0` 的候选属于违约组。

### 回赦与提交

评估开始时所有候选都假设为受害者，`free = cap - 受保护 Pod 需求总和`。回赦顺序固定为：

1. 违约组候选，仍按重要性序。
2. 非违约组候选，仍按重要性序。

对每个候选，仅当 `free - req(candidate) >= req(new pod)` 时回赦并扣减 `free`；否则保留为受害者。选择完成后只删除最终受害者，将其所属预算组的真实预算减一并以 `0` 为下限，然后放入新 Pod。

### 节点选择次序

可行节点按以下固定多级次序比较：

1. 最终仍是受害者的违约组 Pod 数较少者优。
2. 受害者中最高 `prio` 较低者优。
3. 受害者的 `prio + 2^31` 之和较小者优。
4. 受害者数量较少者优。
5. 节点名字节序较小者优。

返回的受害者 ID 按 `prio` 升序排列，同优先级按 ID 字节序升序排列。拒绝时按“参数非法、Pod 已存在、节点不存在、容量不足、无需抢占、无可行节点”的固定顺序返回第一个匹配原因；被拒绝操作不会修改状态。

### 本地验证

如果 `go` 不在 `PATH`，本机工具链位于 `/usr/local/go/bin/go`，可使用：

```bash
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -v ./...
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go vet ./...
```

`TestRandomScenariosAgainstNaiveSimulation` 使用固定种子重放 2000 组随机场景，并与按规则逐步实现的朴素模拟器对照完整状态、节点、受害者列表、拒绝原因和预算余量；加 `-v` 可打印每个场景的输入、输出与判定依据。
