# Dijkstra 推导

同一节点每次发现更短距离都会带着新距离入队，因此队列中可能同时存在多条该节点记录。

弹出 `(node, d)` 时：

- 若 `d == dist[node]`，这是当前最短候选，可松弛其出边。
- 若 `d > dist[node]`，该记录入队后节点又被更短路径改进；它是过期记录，必须直接跳过。

非负权重保证节点第一次以当前最小距离被有效弹出后，之后不可能再出现更短路径。
若不过期跳过，旧的较大距离会重新参与邻居松弛：它可能晚于邻居的更短结论处理，
把已确定或应保持较小值的距离改大，破坏“已确定节点距离不再变化”的贪心不变量，
并造成结果依赖队列入队顺序而抖动。

因此使用 lazy deletion：堆允许多条重复记录，只在弹出时比较 `d` 与当前 `dist[node]`。

## 语义位置

- 最短距离与 BF 参照：`dij/dij.go:65`；测试 `TestShortestPath`
- 不可达为 `+Inf`：`dij/dij.go:60`；测试 `TestShortestPath/no_edges`
- 负权拒绝：`dij/dij.go:40`；测试 `TestShortestPath/negative_edge`
- `ErrBadSrc`：`dij/dij.go:35`；测试 `TestShortestPath/bad_source`
- `ErrBadEdge`：`dij/dij.go:44`；测试 `TestShortestPath/bad_edge`
- 确定性距离：最小堆 + 严格 `<` 松弛；测试 `TestShortestPath`
- 单节点与无边边界：测试 `TestShortestPath/single`、`TestShortestPath/no_edges`
- lazy deletion：`dij/dij.go:74`；测试 `TestStaleRecordMustBeSkipped`
- 松弛上界 ≤ m：有效弹出时每边至多检查一次；测试 `TestRelaxationBound`
- 并发纯函数：测试 `TestConcurrentCallsAreIndependent`
