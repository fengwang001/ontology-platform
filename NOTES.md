# Dijkstra 单源最短路径 — 设计笔记

## 过期记录（lazy deletion）推导

堆中同一节点 v 可能有多条记录：每次 dist[v] 被改进就入队一次 (v, d)。
弹出 (v, d) 时若 d > dist[v]，说明该记录生成后 dist[v] 已被更小的值改进，
此记录为「过期记录」，必须跳过（不展开其邻居）。

为什么不能用过期记录更新邻居：
Dijkstra 的正确性依赖贪心不变量——节点以非递减距离出队，出队时 dist 即最终值，
此后不再变小。过期记录的 d 大于当前 dist[v]，若用它松弛邻居 u，
写入的是 dist[u] = d + w >= 真实最短值，会把已确定的 dist[u] 抬高；
若实现配合 visited 集合（节点只展开一次），错误值将永久残留，
距离被抬高或在多次运行间抖动。
跳过过期记录后，每个节点只按最终最短距离展开一次，
每条边至多被松弛常数次，总松弛次数 <= m，复杂度 O((n+m) log n)。

## 语义条款落点

- 最短距离/不可达 +Inf：dij/dij.go ShortestPath；测试 TestMatchesBellmanFord
- 负权拒绝：dij/dij.go ErrNegativeEdge；测试 TestErrors
- src/边端点越界：dij/dij.go ErrBadSrc、ErrBadEdge（哨兵错误，errors.Is 可区分）；测试 TestErrors
- 确定性：dist 数组唯一，等长最短路不影响取值；测试 TestConcurrentDeterministic
- 单节点/无边图：TestMatchesBellmanFord 表内 single node、no edges 用例
- 过期记录跳过：dij/dij.go 中 `cur.dist > dist[cur.node]` 时 continue；测试 TestStaleRecordsSkipped（含内联错误实现 buggy 对照）
- 松弛次数 <= m：dij/dij.go 非导出计数器 relaxations（原子），经 Relaxations() 读取；测试 TestRelaxationBound
- 并发纯函数：无可变共享状态（仅原子计数器），共享图只读；测试 TestConcurrentDeterministic（-race 通过）
