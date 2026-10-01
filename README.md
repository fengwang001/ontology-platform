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

`Selector` 提供 `AddNode`、`SetBudget`、`Place` 和 `Preempt`，所有操作由同一把互斥锁保护，因此并发调用的结果等价于某个串行顺序。

### 受害者候选

对新 Pod 调用 `Preempt` 时：

- 如果任一节点当前剩余容量即可放下新 Pod，直接返回 `no_preemption_needed`，不评估抢占。
- 否则逐节点独立评估：节点上 `prio >= 新 Pod prio` 的 Pod 全部保留，优先级相等也不可抢占。
- 只有 `prio < 新 Pod prio` 的 Pod 是候选；如果 `cap - 保留需求 < 新 Pod req`，该节点不可行。

### 预算违约划分

候选先按重要性排序：`prio` 降序，同优先级按 Pod ID 字节序升序。每个节点使用调用开始时预算的独立副本，并按上述重要性序尝试消耗预算：

- 空预算组不消耗预算，直接属于非违约组。
- 有预算组的 Pod 使该组副本 `allowed--`；减后仍不小于 0 属于非违约组。
- 减后小于 0 属于违约组。
- 未调用 `SetBudget` 的组初始视为 `allowed=0`。

### 回赦次序

初始假设所有候选都被删除，`free = cap - 保留需求`。随后先按重要性序遍历违约组，再按重要性序遍历非违约组：

- 若 `free - p.req >= 新 Pod req`，回赦该 Pod，并把 `p.req` 从 `free` 中扣掉。
- 否则该 Pod 保持受害者身份。
- 条件中的相等边界可以回赦，即最终剩余容量恰好等于新 Pod 需求。

### 节点比较

可行节点逐级比较，前一级相同才看下一级：

1. 仍是受害者的违约组 Pod 数更少。
2. 受害者中最高 `prio` 更低。
3. 受害者的 `prio + 2^31` 之和更小。
4. 受害者个数更少。
5. 节点名字节序更小。

成功后只删除选定节点上的受害者，放入新 Pod；每个受害者所属预算组的全局 `allowed` 减少一次，但不会降为负数。返回的受害者 ID 按 `prio` 升序、同优先级按 ID 字节序升序排列。

### 本地验证

标准验证：

```bash
gofmt -w selector.go selector_test.go property_test.go
go test ./...
go test -race ./...
go vet ./...
```

随机朴素对照测试固定使用确定种子，共生成 2000 个场景，并在详细日志中打印输入节点、预算、新 Pod、实际输出、oracle 的逐级判定依据及 oracle 输出：

```bash
go test -run TestRandomNaiveOracle2000 -v ./...
```
