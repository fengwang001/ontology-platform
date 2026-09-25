# 拓扑排序与环检测：设计推导

## 三色 vs 两色（第三节推导）

DFS 判环的判据是「边指向当前递归栈上的祖先」，而非「指向访问过的节点」。
节点三态：白=未访问；灰=正在访问（在当前递归栈上，是后续节点的祖先）；
黑=已完成（出边全部处理完，不可能再构成新的环）。
只有指向灰节点的回边构成环：灰祖先沿栈可达当前节点，当前节点再沿该边
回到祖先，路径闭合。两色把黑灰混为一谈，把「再次到达已完成节点」误判
成环。反例（钻石形无环图）：a->b, a->c, b->d, c->d。DFS 从 a 经 b 到 d，
d 标黑；回溯到 a 再走 c，c->d 指向黑色的 d——这只是交叉边而非环。两色
实现见「d 已访问」即报环，把无环图误判成有环（外层循环扫到前一棵 DFS
树已完成的节点时同样误报，链式图也中招）。三色实现见黑色直接跳过，仅
灰色触发 ErrCycle。结论：灰=栈中才是环的判据。

## 语义 → 代码/测试对照

- 拓扑序正确+Kahn 一致：topo/topo.go TopoSort 对照 check/check.go Kahn；check/check_test.go TestTopoSort 逐边验证 pos[u]<pos[v]。
- 环检测 ErrCycle+环上节点：topo.go visit 灰分支 `%w: node %d`；TestTopoSort 的 self loop / 3-cycle 行。
- ErrBadEdge+errors.Is 区分：topo.go 建图前端点校验；TestTopoSort 的越界两行。
- 确定性：节点升序+邻接插入序；TestTopoSort 断言两次调用结果相同。
- 边界（空图空序/链升序）：TestTopoSort 的 empty / chain 行。
- 两色误判钉住：TestTopoSort 的 diamond 行（buggy=true）+ twoColorCycle。
- 边访问计数 ≤m：topo.go 非导出 edgeVisits（atomic）；TestScaleAndRace。
- 并发纯函数、-race 干净：TestScaleAndRace 的 8 goroutine 并发调用。
- demo：cmd/demo/main.go，6 条判定，全 OK 时退出码 0。
