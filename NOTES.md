# NOTES

## 过期记录推导（第三节）
Dijkstra 每次改进 dist[v] 就把新记录 (v, dist[v]) 入队，因此同一节点
在堆中可能同时存在多份记录，且先弹出的一定是其中距离最小的那份。
弹出 (u, d) 时若 d > dist[u]，说明它是被更优记录取代的「过期记录」。
正确做法是 lazy deletion：直接 continue 跳过，不用它松弛任何邻居。
理由：贪心不变量是「节点以最小距离首次弹出时即已确定，dist 之后
不再变化」。过期记录的 d 大于已确定的 dist[u]，若用它更新邻居，
等于允许已确定节点以更大距离再次生效，违反该不变量；邻居的
tentative 距离会被抬高（或被覆盖式实现直接写回更差的值），且过期
记录会沿边继续传播，造成距离抖动。跳过它还有复杂度意义：每个节点
只以最终距离处理一次，每条边至多被松弛常数次，总量 O(m)。
测试钉住：TestStaleEntries 同时断言正确实现距离正确、且内联的
「不跳过过期记录」错误实现 buggy 会返回错误距离。

## 第二节语义位置
1. 最短距离：dij/dij.go ShortestPath；TestMatchesReference 对照 check.Naive。
2. 负权拒绝：dij/dij.go 校验 w<0 返回 ErrNegativeEdge；TestErrors。
3. 错误：ErrBadSrc/ErrBadEdge 为哨兵错误，errors.Is 可区分；TestErrors。
4. 确定性：dist 数组唯一；TestConcurrent 并发结果一致、demo 复核两次调用。
5. 边界：TestMatchesReference 的 single-node / no-edges 用例。
复杂度：dij 内非导出计数器 relaxCount（atomic），Relaxations() 读取；
TestRelaxationBound 断言 n=1000、m=5000 时松弛次数 ≤ m。
并发：TestConcurrent 在 -race 下 8 个 goroutine 共享边切片并发调用。
