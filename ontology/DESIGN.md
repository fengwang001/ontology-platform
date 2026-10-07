# 图遍历服务设计说明

面向本体平台的沿链接类型方向集合的逐跳遍历。支持自环、同一对对象间的多条平行链接，
严格区分「多路径重复到达（菱形汇聚）」与「真实环路」，并把三类终态、三类校验错误、
并发快照一致性与可验证的祖先核对开销全部固化到接口语义中。

## 核心模型

- 对象 `ObjectID`、链接 `Link{ID, Type, Source, Target}`。链接是带方向的实例；
  平行链接靠唯一 `LinkID` 区分，即使 `(Type, Source, Target)` 完全相同也是两条独立路径。
- 输入 `TraverseRequest{Start, LinkTypes: map[类型]方向(out/in), MaxDepth}`。
- 输出每条根到终止点的完整 `Path{Nodes, Links, Reason, Ancestors}`，调用方按 `Reason`
  即可逐条判定终态：
  - `TerminatedBoundary`：自然走到没有候选链接的边界；
  - `TerminatedCycle`：本跳目标已在**当前路径祖先序列**中，真实环路，本方向停止扩展；
  - `TerminatedDepthLimit`：本跳将超过深度上限，截断终止。

## 关键判定（`ontology/traverse.go`）

1. **祖先序列严格按路径定义。** 遍历状态里不存在全局「已访问对象」集合。每条 DFS
   路径持有自己的祖先序列 `nodeSeq` 与集合视图 `ancestor`。对象经另一条不相交路径
   再次到达时，它不在当前路径祖先里，故照常扩展——菱形汇聚不被误判为环。
2. **环判定先于深度判定，且顺序固定。** 每个候选边先做一次祖先探测：
   先查 `target ∈ ancestor`，命中即 `cycle`；未命中再判断 `depth+1 >= MaxDepth`。
   因此同一跳同时满足两条件（含首跳自环且 `MaxDepth=1`）时，对所有输入恒为 `cycle`。
3. **自环首跳即环。** 自环目标就是当前节点，而起点一开始就被放入祖先集合，所以第
   一跳探测必然命中，不存在「先走一跳再判」的窗口。
4. **平行链接独立判定。** 邻接表以 `LinkRef{LinkID, Target}` 为粒度，每条平行链接
   产生独立子帧；一条成环只终止它自己的 `continue` 分支，兄弟分支继续。
5. **祖先集合回溯复用。** 祖先集合是单个 map：下钻 `ancestor[target]=∅`，回溯
   `delete`。兄弟分支天然隔离，每条候选边的集合维护与探测均为 O(1)。

## 深度语义

`MaxDepth` 为允许走过的最大跳数。走到第 `MaxDepth` 跳即按 `depth-limit` 终止
（`MaxDepth=1` 时只走一跳）。深度截断只作用在仍可扩展的边上；该节点若无候选边则
为 `boundary`。环始终优先。

## 校验错误（互斥、固定次序）

固定为：`ErrStartObjectNotFound` → `ErrEmptyOrUndefinedLinkTypes`
（方向集合为空 / 含未定义类型 / 方向值非法）→ `ErrInvalidMaxDepth`。
拒绝时直接返回，不生成任何 `Path`、不写遍历日志、不做任何扩展，因此无部分结果。

## 快照与并发（`ontology/graph.go`）

- `MemGraph` 的全部状态（对象、类型、链接、出入邻接表）放在一个不可变 `graphState`
  中，经 `atomic.Pointer` 发布；增删改采用 copy-on-write（写锁仅保护克隆→修改→发布）。
- `Snapshot()` 是一次 O(1) 的原子指针加载，返回不可变视图；遍历全程只读该视图，
  与后续 `AddLink/DeleteLink` 完全无共享可变状态，不互相阻塞。
- 因此一次遍历的结果等价于「开始时刻某个确定快照」上的串行结果，之后的修改既不会
  混入，也不会造成重复或遗漏。旧快照由 GC 在无人引用后回收（被放弃的持久化方案见下）。

## 祖先核对开销：结论与可验证方式

结论：单次遍历中用于判定环路的祖先序列核对次数 = 遍历实际展开的候选边数，每次核对
为 O(1) 哈希探测，**与图中对象、链接总数无关**，只与本次遍历足迹有关。

可在测试中复现的核对方式：
- `TraverseResult.AncestorChecks` 暴露总核对次数；每次候选边扩展恰好自增一次。
- `TestAncestorCheckCountIsLocalAndConstantPerEdge`：
  1. 固定小图上增加 500 个不可达对象与自环链接，核对次数保持不变（证明不随总图规模
     线性增长）；
  2. 精确断言小图核对次数 == 展开边数（该例为 2）；
  3. 在 100 张随机图上验证「核对次数 == 遍历树中不同链接前缀数」这一代数恒等式。
- `BenchmarkTraverseHashAncestors` 与 `BenchmarkNaiveLinearAncestors`（2000 节点长链）
  对照：O(1) 哈希探测版本约 12 ms/次，朴素线性扫描祖先序列版本约 24 ms/次，链越长
  差距越大（线性扫描为 O(路径长度) 每跳）。

## 被放弃 / 被拒绝的方案

- **用全局 `visited` 集合判环**：直接否决。它会把菱形汇聚（同一对象经两条不相交路径
  到达）误判为环，与需求根本冲突。
- **每跳整表复制祖先集合**：曾作为最简单的分叉隔离方案实现，但长链上是 O(路径长度)
  的每跳复制，基准显示比朴素实现还慢。已替换为回溯式单集合（正确性不变、每跳 O(1)）。
- **朴素线性扫描祖先切片作为生产实现**：保留为独立参照实现 `NaiveTraverse`（刻意不用
  哈希、每次全量扫描），仅用于随机图差分测试与基准对照，不进生产路径。
- **读时加全局 RWMutex 拿快照**：正确性可接受，但遍历期间会与增删改互相阻塞，放弃；
  改用不可变状态原子发布，读与写互不阻塞。
- **永久保留所有历史版本（持久快照/版本链）**：隔离性更强但内存无界，需要额外 GC
  策略；当前只要求单次遍历期间的快照，COW + GC 足够，故不引入。
- **用 BFS/visited 去重压缩输出**：会丢失「同一对象经不同路径多次到达」这一必须如实
  返回的路径，拒绝；遍历枚举完整路径多重集。

## 本地验证方法

需 Go 1.26+（`go version`）。

```bash
go test -race -v ./ontology/          # 全部用例（含竞态检测）
go test -cover ./...                  # 覆盖率
go vet ./... && gofmt -l .            # 静态检查与格式
go test -run xxx -bench . -benchtime=200x ./ontology/   # 祖先核对开销对照基准
```

关键用例与需求的对应关系：

| 需求 | 测试 |
| --- | --- |
| 自环首跳即环 | `TestSelfLoopDetectedOnFirstHop` |
| 环优先于深度截断 | `TestCycleWinsOverDepthLimit` |
| 平行链接部分成环部分正常 | `TestParallelLinksMixedCycleAndClean` |
| 菱形汇聚不误判 | `TestDiamondMergeIsNotCycle` |
| 环与深度截断共存于一次遍历 | `TestCycleAndDepthTruncationCoexist` |
| 校验错误次序/互斥 | `TestValidationOrder` |
| 并发修改快照一致性 | `TestConcurrentMutationSnapshotIsolation` |
| 与朴素实现随机差分（200 图） | `TestNaiveDifferentialOnRandomGraphs` |
| 祖先核对次数可验证 | `TestAncestorCheckCountIsLocalAndConstantPerEdge` |
| 日志含输入/终态/祖先序列 | `TestLoggingRecordsInputPathTerminalAndAncestors` |
| 方向集合 out/in 与快照删除 | `TestDirectionsAndDeleteAndSnapshot` |

## 日志

`MemoryLogger` 记录每次遍历：输入（起点、深度、类型方向集合）、`AncestorChecks`，
以及每条路径的节点序列、链接序列、终态分类和「据以判定的祖先序列」。渲染示例：

```
traversal #1 start=a maxDepth=5 directions={t:out} ancestorChecks=2
  path=[a -> b -> a] links=[e1, e2] terminal=cycle ancestors=[a -> b]
```

## 局限与边界

- 遍历枚举完整路径多重集，图中大量平行链接/稠密结构会使输出路径数指数增长；调用方
  应通过 `MaxDepth` 与类型方向集合限定足迹（测试生成器也刻意保持稀疏）。
- `MemGraph` 为进程内实现；对接持久化本体时，`Snapshot` 接口可由 MVCC/版本号读视图
  实现，遍历算法无需改动。
