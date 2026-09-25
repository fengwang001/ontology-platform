# NOTES: Dijkstra 过期记录推导

## 过期记录（lazy deletion）推导
- 节点 v 的距离每被改进一次，就向堆压入一条新记录 (v, d)，旧记录仍留在堆里，成为「过期记录」。
- 不变量：弹出 (v, d) 时若 d == dist[v]，v 此刻的距离即最终最短距离（非负权 + 最小堆保证，贪心不变量）。
- 因此弹出时若 d > dist[v]，该记录必然过期：v 已用更小的 d 被处理过，邻居已被更优值松弛过。
- 必须直接跳过（lazy deletion）。若用过期记录去更新邻居，等于让「已确定节点」再次参与松弛，违反贪心不变量；
  一旦实现中存在覆盖式写入（而非取 min），过期的大 d 会把邻居距离抬高，且后续过期记录会让 dist 抖动。
- 跳过是安全的：过期记录能提供的 nd = d + w >= dist[v] + w >= 既有 dist[邻居]，不可能产生更优解。

## 语义落点（代码位置 / 测试）
- 最短距离 + 不可达 +Inf：dij/dij.go ShortestPath；check.TestMatchesReference（对照 check.Reference 的 Bellman-Ford）。
- 负权拒绝 ErrNegativeEdge：dij/dij.go；check.TestErrors/negative。
- ErrBadSrc / ErrBadEdge（errors.Is 可区分）：dij/dij.go；check.TestErrors。
- 确定性：dist 数组唯一，check.TestEdgeCasesAndDeterminism（含等长双路径 tie 图重跑）。
- 边界（单节点 / 无边）：check.TestEdgeCasesAndDeterminism。
- 过期记录跳过：dij/dij.go 中 `if it.dist > dist[it.node] { continue }`；check.TestStaleEntries
  （对照 check.go 内联错误实现 buggy：不跳过过期记录且覆盖式更新，dist[4] 被抬高为 101 而非 4）。
- 松弛次数上界 <= m：dij.Relaxations 计数；check.TestRelaxationBound。
- 并发纯函数：check.TestConcurrent（-race）。
