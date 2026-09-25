# Ontology 对象图遍历器设计

## 0. 模块边界

- `graph`：有向图，节点用名字标识；维护邻接表 `out` 与反向表 `in`。
- `traverse`：纯遍历序，`Walk(start, dir)` 返回从 `start` 按 `dir`（out/in/both）可达的节点序列（无界、不去重由调用方负责时本包内部去重）。
- `bounded`：在遍历序上施加 `MaxDepth(d)`、`Limit(n)`，返回节点序列 + 三态结果。
- `api`：唯一对外入口 `Traverse(req)`，做参数校验并组装前三包。
- 遍历算法选择 **BFS**：两种可选是 BFS / DFS。深度在 BFS 中天然等于「距起点最短边数」，`MaxDepth` 语义无歧义；DFS 的深度受访问顺序影响，同一节点在不同分支深度不同，去重后无法稳定表达「深度 ≤ d 都要访问到」。最终选 BFS。

## 1. 推导一：环检测的三色标记

DFS 三色：白（未访问）、灰（在当前递归栈上，开始但未完成）、黑（递归已返回，彻底完成）。

- 可选口径 A：**遇到任何「已访问」节点（灰或黑）就报环**。
  - 后果：菱形依赖 A→B、A→C、B→D、C→D 会误报。先走完 A→B→D 时 D 已黑，回到 A→C 再到 D，D 是黑而非环；口径 A 却报环。
- 可选口径 B：**只有边指向灰节点才报环；指向黑节点只是两条路径汇合，不报环**。
  - 后果：灰节点必在当前递归栈上，`x → 灰 y` 意味着沿栈从 y 能回到 x，再加上 x→y 构成有向环；黑节点的所有后代都已结束，不可能再回到当前栈。菱形判 false，A→B→C→A 中 C→A（A 灰）判 true。
- 最终选择：**口径 B**。只对「目标为灰」报环；每条边仅在其源节点被 DFS 考察时扫描一次，复杂度 O(V+E)。

## 2. 推导二：Limit 截断与环

`Limit(n)` 限制输出节点数，节点只输出一次（首次到达为准）。

- 可选口径 A：**已输出 == n 即记 `LimitCut`**。
  - 后果：可达节点数恰好等于 n 时，遍历其实自然走完，却被误报截断；环上节点因去重提前耗尽也会被误判。
- 可选口径 B：**先按 BFS 完成「发现」（受深度约束），判定是否还有「已遇到但因额度未输出」的节点；有则 `LimitCut`，无则 `Complete`**。
  - 后果（最终选择口径 B）：可达 ≤ n 时无遗留，判 `Complete`（含可达 == n）；环上未走到的节点在发现队列里，只要额度用尽且仍有未输出节点，判 `LimitCut`。
- 判定式：**`unreturned = 可达集合中发现了但未输出的节点数`；`unreturned > 0` 才是 `LimitCut`**，绝不使用 `emitted == n`。

## 3. 推导三：MaxDepth 截断与环

起点深度 0；环 A→B→C→A 中 A 会在深度 0 与深度 3 两次到达。

- 可选口径 A：**允许重复入队/输出**。
  - 后果：环上无限循环或重复计数，深度上限失去意义。
- 可选口径 B：**节点只按首次到达（最短深度）记录一次；但深度约束只截断「会发现新节点」的越界边**。
  - 后果（最终选择）：A 深度 0 已黑（已发现），深度 3 的回边指向旧节点，不产生新节点，不触发截断；d ≥ 环上最长最短距离时环不造成无限，判 `Complete`。
- `DepthCut` 判定式：**扫描深度恰好为 d 的节点时，存在一条边指向「尚未发现」的节点**（该节点本应在深度 d+1）。指向已发现节点（含环回边）不算截断。

## 4. 三态优先级与算法

单次 BFS 同时产出结果，三态互斥：

1. BFS 按层推进（深度 = 最短距离），发现即入「已发现集合」，去重。
2. 深度：扫描深度 d 的节点时若有越界发现边，置 `depthPending=true`；该边目标不展开。
3. 输出：从已发现序列中取前 `n` 个输出；未输出部分计入 `unreturned`。
4. 判定：`unreturned > 0` → `LimitCut`；否则 `depthPending` → `DepthCut`；否则 `Complete`。
   - 优先级理由：额度先耗尽时用户能观察到的直接原因是 limit；额度未耗尽才可能归因于深度。

哨兵错误：`ErrLimitCut`、`ErrDepthCut`，支持 `errors.Is`；`Complete` 为 nil。另用只读枚举 `Status` 便于 switch。

## 5. 跨模块不变量（非导出计数器证明）

- `bounded` 维护计数器，断言 `emitted + unreturned == 可达节点数`（按有效 dir/深度；环上每节点只计一次）。
- `graph` 维护 `edgeExaminations` 计数器，断言 `edgeExaminations ≤ V + E`；V=100、V=10000 两档验证。
- 任一遍历恰好落三态之一，且 `errors.Is(err, ErrLimitCut)` 与 `errors.Is(err, ErrDepthCut)` 互斥可分。

## 6. 包接口

- `graph.New() *Graph`；`AddNode(name string) error`；`AddEdge(from, to string) error`（端点不存在、自环、重边均报错）；`HasCycle() bool`。
- `traverse.Dir`（`DirOut/DirIn/DirBoth`）；`Walk(g, start, dir) ([]string, error)`，BFS 去重，层内按邻接表插入序。
- `bounded.Run(g, start, dir, maxDepth, limit int) (nodes []string, status Status, err error)`。
- `api.Traverse(req Request) (*Response, error)`：起始节点不存在、非法 dir、`maxDepth<0`/`limit<=0` 返回校验错误；`api` 是唯一入口。
