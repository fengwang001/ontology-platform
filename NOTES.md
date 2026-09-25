# 拓扑排序与环检测 — 设计笔记

## 三色标记 vs 两色标记（第三节推导）

DFS 判环的充要条件：遍历中遇到一条指向「当前递归栈上节点」的回边。
因此节点必须区分三态：白=未访问，灰=在当前递归栈中，黑=已完成（已出栈）。

两色（只记「访问过/没访问过」）误判的反例——钻石形无环图：
a→b, a→c, b→d, c→d。DFS 从 a 出发经 b 到达 d，d 完成后标黑；
随后经 c 再次到达 d。若把「已访问」当作「环」，d 被误报成环上节点，
正常无环图被拒绝。线上事故即此：黑节点只是「已完成」，后续路径经过它
不等于回边。只有「灰=栈中」才是环的判据：遇到灰节点说明存在从它到
当前节点的路径，加上当前这条边即构成环。

三色 DFS 每节点入栈置灰、出栈置黑；邻接点灰→发现环，黑→跳过，
白→递归。每条边恰被检查一次，复杂度 O(n+m)。

## 语义落点（第二节各条）

- 拓扑序正确：`topo.TopoSort`（topo/topo.go），对照 `check.Kahn` 与 `check.validOrder`（check/check.go）；测试 `TestTopoSort`
- 环检测：`ErrCycle` + `CycleError.Node` 指出环上节点（topo/topo.go）；测试 `TestTopoSort`（cases 中 self-loop/two-cycle/triangle 行）
- 越界边：`ErrBadEdge`，两类哨兵错误用 `errors.Is` 区分；测试 `TestTopoSort`（neg-src/dst-oob 行）
- 确定性：节点按编号升序、出边按添加顺序遍历；测试 `TestEdgeVisitsAndConcurrency`（并发结果全一致）
- 边界：空图返回空序、链返回升序（cases 表 empty/chain 行）；测试 `TestTopoSort`
- 钻石图不误判 + 内联两色实现误报对照：测试 `TestDiamondNoFalseCycle`
- 边访问计数 ≤ m：非导出计数器经 `topo.EdgeVisits` 暴露；测试 `TestEdgeVisitsAndConcurrency`
- 并发纯净（-race）：`TopoSort` 无可变共享状态；测试 `TestEdgeVisitsAndConcurrency`
