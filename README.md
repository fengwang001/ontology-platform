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

## 多维资源成组装箱

`placement` 包提供节点注册、作业放置、移除和查询：

- `NewPlacer(w io.Writer) *Placer`：创建放置器；写入 `w` 的日志包含每次输入、输出、所选节点、失败副本序号和判定依据。
- `AddNodes([]NodeSpec)`：注册带 `ID`、`CPU`、`Memory` 总量的节点；节点容量必须为正，节点标识不得与既有节点或同批次节点重复。
- `Place(JobSpec)`：原子放置一个作业的全部副本；`Replicas=k`、`CPUPerReplica`、`MemoryPerReplica` 和 `MaxPerNode=a` 都必须为正。
- `Remove(jobID)`：释放在册作业的全部副本；作业不存在时拒绝。
- `JobPlacement(jobID)` 与 `Nodes()`：返回复制后的结果快照，可与放置、移除并发调用。

### 评分规则

副本按序号 `0..k-1` 逐个选择节点。候选节点必须同时满足：

- 当前剩余 CPU 和内存都能容纳该副本；
- 该作业在该节点上的现有副本数小于 `a`。

对每个候选，计算“放置后 CPU 剩余占比”和“放置后内存剩余占比”，取两者中较大者作为该节点评分；评分越小越优先。分数比较使用 `math/big` 做交叉相乘，不使用浮点；评分相同则取节点标识升序。后一副本看到的是前一副本已更新后的剩余量，因此同一节点可连续被选择，直到达到 `a` 或任一维度放不下。

### 成组语义

`Place` 是全有或全无的：贪心过程中任一副本没有候选节点，立即撤销本次作业此前选中的全部副本，不登记该作业，也不改变其他在册作业。即使通过重排已有作业或尝试别的本次分配方式可以放下，也不会回溯或移动其他作业。

不可放置分为两类，两类条件同时满足时按永久不可放处理：

- 永久不可放：空载下没有任何节点能容纳一个副本，或 `k > a*n`，其中 `n` 是空载下能容纳单副本的节点数；对应 `ErrPermanentlyUnfit`。
- 暂时不足：空载判定可行，但当前占用导致固定贪心在某个副本上没有候选节点；对应 `ErrTemporarilyInsufficient`。

参数与状态错误通过哨兵值区分，可用 `errors.Is` 判断：

- `ErrInvalidNodeCapacity`、`ErrDuplicateNodeID`；
- `ErrInvalidReplicas`、`ErrInvalidResourceRequest`、`ErrInvalidReplicaLimit`；
- `ErrDuplicateJob`、`ErrJobNotFound`；
- `ErrPermanentlyUnfit`、`ErrTemporarilyInsufficient`。

参数类多因并存时，只按“节点参数、作业参数、作业重复”的顺序报告第一个原因；被拒绝的操作不会改变节点占用。所有公开操作由同一把锁保护，结果等价于某个合法的串行顺序，相同节点与操作序列重放会得到相同放置。

### 本地验证

```bash
# 单元测试
GOCACHE=/tmp/ontology-go-cache go test -v ./placement

# 竞态检测
GOCACHE=/tmp/ontology-go-cache go test -race ./...

# 格式化与静态检查
gofmt -w placement
GOCACHE=/tmp/ontology-go-cache go vet ./...
```
