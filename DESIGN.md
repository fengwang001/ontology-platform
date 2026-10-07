# 图遍历服务设计说明

面向场景：本体平台上的对象/链接图，允许**同一链接类型自环**（`source == target`）与**同一对对象间多条平行链接**。服务必须严格区分两种现象：

- **多路径重复到达**：对象经另一条不相交路径到达过，但不在当前路径的祖先序列中——合法，继续扩展；
- **真实环路**：对象出现在当前路径自己的祖先序列中——立即终止该方向并记录环路。

实现位于 `traversal/` 包。

## 1. 核心模型与关键取舍

### 1.1 判环依据：每条路径独立的祖先集合，而非全局 visited

`traverser`（`traversal/engine.go`）在 DFS 中维护：

- `ancestors map[ObjectID]int` + `pathNodes []ObjectID`：**仅属于当前这一条递归路径**；
- 进入下一跳前先做祖先归属判定；非环则把目标加入 `ancestors`，递归返回后从 `ancestors` 删除（进入加入、回溯删除）。

因此：

- 菱形 `A->B->D, A->C->D` 中，两个分支各有独立祖先集（`{A,B}` / `{A,C}`），`D` 对两者都不是祖先，两条路径都正常终止；
- 绝不存在「遍历过程中全局已访问对象集合」参与判环的路径。

**被放弃的方案 A：全局 `visited` 集合判环。** 该方案会把菱形汇聚在第二次到达 `D` 时误报为环，直接违反需求；任何形如「到达即标记」的剪枝都被禁止用于判环（`visited` 只能用于与判环无关的去重统计，本实现干脆不引入，避免歧义）。

**被放弃的方案 B：朴素地在每跳线性扫描祖先切片。** 其单次归属判定为 O(路径长度)，总核对开销随路径长度平方增长，无法满足「不随对象/链接总数增长」的可验证要求。该实现被保留为**独立参照实现** `NaiveTraverse`（`traversal/naive.go`），但仅用于测试对照，不进入生产路径。

### 1.2 三类终态与固定优先次序

每条候选边依次经过两道判定（`traversal/engine.go` 的 `dfs`）：

1. **环路判定优先**：目标在当前祖先集合中（自环在第一跳即命中，因为起点初始化时就在祖先集合中）→ 产出 `cycle` 路径，闭合跳计入路径并填充 `CycleInfo`，不再沿该候选扩展；
2. **深度判定其次**：进入节点后若 `depth == MaxDepth` → 产出 `depth_limit` 路径；
3. 其余：枚举允许方向上的候选链接；候选为空 → `boundary`。

因此当「同一跳同时成环且落在深度上限」（如 `MaxDepth=2` 的 `A->B->A`）时，**固定报告 `cycle`**。该次序写在每跳的处理顺序中，与 DFS 调度顺序无关；候选枚举顺序被完全确定化（按 `(链接类型, 方向, 目标对象, 链接ID)` 排序），保证同输入同输出、不因 map 迭代次序漂移。

**取舍说明：** 深度上限按「跳数」定义，起点在第 0 跳，`MaxDepth` 必须为正整数；到达深度上限的节点即使仍有出边也归类为 `depth_limit`，但进入该节点的那一跳已经先经过环路判定，所以不会出现「应报环却被深度吞掉」。

### 1.3 自环与平行链接

- 自环：`ancestors` 初始化即含起点，第一跳枚举到 `A->A` 立即命中 `cycle`，不存在「先扩展一跳再判」的路径；
- 平行链接：邻接表按链接实例（`LinkID`）保存，不做 `(源,目标,类型)` 去重，每条链接是独立候选；同一层某条链接闭合为环，`continue` 后其余链接照常判定（见 `TestParallelLinksMixed`）；
- `DirBoth`：对同一链接类型分别枚举入、出两个方向的候选；沿同一条边立即反向回到上一跳会判为环（无向遍历的标准往返），这是明确的语义而非误判。

### 1.4 输入校验：三类互斥错误、固定次序

`validateRequest` 在**开始时刻的快照**上按以下次序判定，一次只返回第一类：

1. `ErrStartObjectNotFound`：起始对象不存在；
2. `ErrInvalidDirections`：方向集合为空、方向值非法、或包含未登记的链接类型；
3. `ErrInvalidDepth`：`MaxDepth <= 0`。

校验在任何扩展之前完成并直接返回，因此被拒绝的请求**不展开任何节点、不产生部分路径**；仍会记录一条 `Accepted=false` 的日志（输入与错误原因），但不附带路径，也不产生遍历资源消耗。

### 1.5 并发修改与快照一致性

`Graph`（`traversal/graph.go`）采用 **copy-on-write 不可变快照 + 原子指针替换**：

- 每次结构修改在互斥临界区内拷贝三张基础 map（`objects` / `linkTypes` / `linksByID`），版本号加一，`atomic.Pointer[snapshot]` 发布；
- 已发布快照的 map 永不被修改；
- `Traverse` 开始时原子读取一次快照指针，整个 DFS 只引用该快照，因此结果**必然等价于开始时刻某个确定快照上的遍历**，遍历期间的链接增删落在更新的快照上，既不混入也不造成重复/遗漏；
- 邻接索引不放在修改热路径上维护，而是在快照**首次被遍历时**通过 `sync.Once` 惰性构建并随快照只读缓存（早期版本每次增删都全量重建邻接表导致建图 O(n²)，已废弃；见 §4）。

单条增删各自原子发布；`Graph.Batch` 允许一批修改在同一临界区内校验并**一次发布**（构建大图或事务式变更使用），批内任一项非法则整批拒绝、不发布版本。

### 1.6 祖先核对复杂度的可验证证据

需求要求「真实环路判定所触及的祖先核对次数不随对象/链接总数线性增长」，且须可在测试中复现核对。本实现通过**计数器契约**给出确定性证据（`Stats`，`traversal/types.go`）：

- `CandidateEdges`：枚举处理的候选链接数（平行链接各计一次）；
- `AncestorChecks`：祖先归属判定次数，恒等于 `CandidateEdges`；
- `AncestorProbes`：判定内部的比较探测次数。基于每路径哈希集合的实现每跳恰为 1 次 map 探测，故 **`AncestorProbes == AncestorChecks == CandidateEdges`**，与路径长度、总图规模均无关。

对照朴素实现在线性扫描祖先切片时 `AncestorProbes` 随深度线性累加（同一条 50 跳链路上至少为 `50*51/2`）。测试直接断言这一差异，使「O(1)/跳」成为每次 CI 都复现的可核对事实，而非口头承诺。

## 2. 结果结构

- `TraversalResult{SnapshotVersion, Paths, Stats}`；
- 每条 `TraversedPath` 含 `Nodes`（长度 = `Links`+1）、`Links`（`LinkID`/类型/实际方向）、`Depth`、`Status`；
- `Status == cycle` 时 `Cycle` 非空：`RepeatedObject`、`AncestorIndex`（在祖先序列中的下标）、`ClosingLink`、`AncestorSequence`（判定时使用的祖先对象序列，不含被重复到达的目标）；
- 调用方对每条路径独立判断终态，`cycle` 与 `depth_limit` 不会混为一类。

## 3. 日志

`Logger` 接口 + `MemoryLogger`（测试断言用）与 `TextLogger`（可读输出）。每次遍历记录：输入（起点、链接类型方向集合、深度）、是否接受、错误（若拒绝）、快照版本、每条路径的终态分类与（环路路径的）祖先序列/重复对象/下标，以及 `Stats`。

## 4. 被放弃/演进掉的方案汇总

1. **全局 visited 判环**：误杀菱形汇聚，违反核心语义，禁止。
2. **每跳线性扫描祖先**：判环 O(路径长度)/跳，无法满足复杂度要求；降级为仅测试用的朴素参照。
3. **修改时全量重建邻接索引**：每条 `AddLink` 重建全部邻接表，批量建图 O(n²)（2 万对象规模下测试分钟级超时）；改为快照首次遍历时 `sync.Once` 惰性构建，修改路径只拷贝基础 map；需要一次性构建大图时使用 `Batch`。
4. **边遍历边持锁读图**：会把并发修改与遍历耦合，且锁持有时间不可控；改为不可变快照 + 原子指针，遍历无锁。

## 5. 本地验证方法

需要 Go 1.26+（仓库要求 1.26.5）。

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 详细用例
go test -race -v ./traversal

# 覆盖率
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

# 静态检查
gofmt -l . && go vet ./...

# 大规模基准（10 万对象 / 20 万链接，遍历只接触可达小子图）
go test -bench=BenchmarkTraversalLargeGraph -run='^$' ./traversal/
```

测试与需求的对应关系：

| 需求 | 测试 |
| --- | --- |
| 自环首跳即判 | `TestSelfLoopDetectedOnFirstHop` |
| 平行链接部分成环部分不成环 | `TestParallelLinksMixed` |
| 菱形汇聚不误判 | `TestDiamondConvergenceIsNotCycle`、`TestRealCycleVsMultipath` |
| 深度截断与环路同次遍历 | `TestCycleAndDepthLimitInSameTraversal` |
| 同跳既是环又触顶 → 固定报环 | `TestCycleWinsOverDepthAtSameHop` |
| 入/出/双向、多链接类型 | `TestInboundAndBoth`、`TestMultipleLinkTypes` |
| 三类错误次序与拒绝即无部分结果 | `TestValidationErrorPrecedence`、`TestRejectedRequestLoggedWithoutPaths` |
| 并发修改的快照一致性 | `TestSnapshotIsolationUnderConcurrentMutation`（200 轮并发增删下逐路径比对所声明快照）、`TestSnapshotExactEquivalence` |
| 祖先核对 O(1)/跳的可复现证据 | `TestAncestorCheckConstantPerHop`、`TestProbeCounterPerEdgeContract`、`TestResultSizeIndependentOfUnrelatedGraphGrowth` |
| 与朴素实现的大规模随机对照 | `TestRandomDifferentialAgainstNaive`（400 个带种子的随机图，含自环/平行/多类型/随机方向/随机深度） |
| 日志含输入/终态/祖先序列 | `TestLoggingRecordsInputsStatusesAndAncestors`、`TestTextLoggerOutput` |
| 批量变更原子性与级联删除 | `TestBatchAtomicityAndValidation`、`TestSingleOpValidation` |
