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

## 集群缩容执行器

`scaler` 包提供可并发调用的节点与 Pod 登记、缩容候选判定和单次移除执行：

- `New(P, T, MinNodes)`：`P` 为严格低利用百分比阈值，`T` 为持续毫秒数，`MinNodes` 为节点数下限；参数越界返回配置非法。
- `AddNode`：校验节点名为空、CPU/内存可分配量是否在 `1..10^12`，再检查重名。
- `AddPod`：按非法参数、Pod 重名、节点不存在、容量不足的顺序只返回第一个错误；所有 Pod（含 daemon）共同占用节点容量。
- `RemovePod`：Pod 不存在时返回错误；登记和删除操作不会修改任何 `since`。
- `Tick(now)`：拒绝负数时间和时钟回退，接受后按单调时钟推进。

### 候选判定

每轮按节点名字节序评估。节点上的 daemon 请求不计入利用率，但仍占用真实容量和模拟迁移目标容量；pinned Pod 会直接阻止节点移除。

低利用必须在 CPU 和内存两个维度都严格成立：

```text
normalCPU * 100 < P * nodeCPUAllocatable
normalMem * 100 < P * nodeMemAllocatable
```

normal Pod 按“CPU 降序、内存降序、Pod ID 字节序”排序并逐个寻找落点。目标节点按名字节序选择第一个能同时容纳 CPU 和内存的节点，目标不能是候选自己、本轮已经判定可移除的节点，也不能是本轮已经接收过模拟迁入的节点。

候选成功前，其模拟迁入只暂存在本轮状态中；任一 Pod 放不下时，该候选本次产生的 CPU/内存临时占用全部撤销。候选成功时落点保留，后续候选必须把这些请求计入目标空闲量。

### 计时和移除

评估成功的节点首次记录 `since=now`，之后只要持续可移除就保持原值；评估失败会清除 `since`。本轮接收过模拟迁入的节点本身不可移除，并清除已有 `since`。

只有节点总数大于 `MinNodes`，且 `now-since >= T` 时才能执行。每轮至多移除一个节点，选择规则是 `since` 最小；相同 `since` 取节点名字节序最小者。执行时删除被移除节点上的 daemon Pod，并把 normal Pod 按本轮记录的落点迁移到目标节点；空节点可以直接移除。

### 复现和验证

测试包含阈值严格小于、CPU/内存双维度、daemon 容量占用、模拟迁入占位、失败撤销、接收者清除 `since`、持续时长边界、节点下限、同名 `since` 并列、空节点、错误优先级、拒绝原子性和并发调用。

`TestRandomScenariosMatchNaiveSimulation` 使用固定随机种子重放 2000 组场景，并与按规则逐步实现的朴素模型逐轮比较移除节点、迁移落点和最终状态。使用 verbose 模式可查看每组输入、输出和候选判定依据：

```bash
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -race -v ./scaler
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
```
