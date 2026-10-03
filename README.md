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

## 增量拓扑序维护器（`topo` 包）

`topo.Maintainer` 在有向无环图上随节点与边的增删持续维护一个**确定的**
拓扑序。每个存活节点持有互不相同的非负整数序值（序值小者在前，序值
之间允许有空洞），任意时刻每条存活边都满足 `ord(起点) < ord(终点)`。
所有方法可并发调用，结果等价于某个串行顺序；相同的操作序列重放得到
完全相同的 `Order` 与环路见证。

### 受影响区间的划定

`AddEdge(u, v)` 时：

- 若 `ord(u) < ord(v)`，直接加边，不改任何序值（touched 记 0）。
- 否则令 `lb = ord(v)`、`ub = ord(u)`（`u == v` 直接按成环拒绝）：
  - **δF**：从 `v` 出发沿现有出边可达、且序值 `<= ub` 的全部节点（含 `v`）。
    若 `u ∈ δF` 则成环，拒绝且不留下任何改动。
  - **δB**：沿现有入边能到达 `u`、且序值 `>= lb` 的全部节点（含 `u`）。

只有 `δF ∪ δB` 中的节点会被重排，区间 `[lb, ub]` 之外的节点序值不变，
因此绝不是整图重新拓扑排序。非导出计数器 `touched` 记录成功重排标记
过的节点数（恰为 `|δF| + |δB|`）；被拒绝的操作不改变 `touched`，且成环
前的扫描只触及 `[lb, ub]` 内的节点。例如 100000 节点的长链上，在相邻
序值之间加边时 `touched` 恒为 2。

### 池的分配规则

取 `δB` 与 `δF` 全部节点的**旧序值**构成池，升序排列。把 `δB` 按旧序值
升序、接着 `δF` 按旧序值升序排成一个节点序列，序列中第 i 个节点分得
池中第 i 小的序值；其余节点序值不变。`Moved` 为序值实际发生变化的
节点，按新序值升序。

### 环路见证

新增边 `u -> v` 会成环时，返回 `*CycleError`，其 `Path` 为从 `v` 到 `u`
**边数最少**的路径；边数相同取节点编号序列**字典序最小**者，写作
`[v ... u]`，加上被拒绝的 `u -> v` 即成环。`u == v` 时见证为 `[u]`。
见证基于拒绝那一刻的图（批量场景下含批内先前已加入的边）。

### 序值空洞

`AddNode` 返回的编号从 0 起严格递增，删除后不复用，新节点序值等于其
编号。`RemoveNode` 删除节点及其全部关联边，其他节点序值不变，被删除
节点的序值成为空洞、不再被分配。`RemoveEdge` 不改任何序值。

### 批量接口

`AddEdges(list)` 接受 1 到 1000 条边（否则报 `ErrBatchSize`），按顺序
逐条套用 `AddEdge` 的全部规则；任一条被拒绝则整批**原子回滚**（序值、
边集合、创建计数、touched 全部恢复到批前），返回 `*BatchError`（失败
下标 + 原因）。全部成功时返回批级 `Moved`（批前后序值不同的节点，按
新序值升序；中途移动但批末回到原值的节点不列入）与逐条的
`(|δF|, |δB|)` 列表。

### 可区分的拒绝原因

`ErrNodeNotFound`、`ErrEdgeExists`、`ErrEdgeLimit`、`ErrNodeLimit`、
`ErrEdgeNotFound`、`ErrBatchSize` 与 `*CycleError`（携带见证）。
`AddEdge` 按节点不存在、边已存在、边数已满、成环的顺序只报第一个
（`u == v` 且节点存在时按成环）。

### 本地验证

```bash
# 全部单元测试（含规格示例逐条回放）
go test ./topo/

# 随机对照：2000 组随机操作序列（含批量）与朴素模拟逐步对照，
# -v 日志打印每步的输入、输出与判定依据
go test ./topo/ -run TestRandomAgainstModel -v

# 竞态检测（并发等价于某个串行顺序）
go test -race ./topo/
```
