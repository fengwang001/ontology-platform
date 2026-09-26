# NOTES — DAG 最小路径覆盖（ontology-754）

## 推导：n=6，边 0→1、0→2、2→1、3→4、4→5

最大匹配取 {0→2, 2→1, 3→4, 4→5}（右 1 让给左 2，左 0 改配右 2）。

| 节点 | 路径前驱（匹配入边） | 路径后继（匹配出边） | 角色 |
|---|---|---|---|
| 0 | 无 | 2 | 起点 |
| 1 | 2 | 无 | 终点 |
| 2 | 0 | 1 | 中间 |
| 3 | 无 | 4 | 起点 |
| 4 | 3 | 5 | 中间 |
| 5 | 4 | 无 | 终点 |

最大匹配 |M|=4，覆盖数 = 6−4 = 2，路径为 [0,2,1] 与 [3,4,5]。

- (甲) 把每条匹配边单独当一条路径：得 4 条（正确 2 条）——没把首尾相接的匹配边合并成链。
- (乙) 贪心（左编号升序、取编号最小空闲右、不回溯）：0 先占右 1，左 2 无路可走，只得 3→4、4→5，|M|=3、覆盖 3（正确 |M|=4、覆盖 2）。
- (丙) 再加孤立节点 6（n=7，无边）：正确覆盖 3（[6] 单独一条）；若重建只从「有匹配后继」的节点出发，节点 6 被漏，只返回 2 条。

## 四条不变量：保证位置 / 钉住测试

1. 覆盖合法（每条是真实链、两两不相交、并集恰为全部 n 个顶点）：`mpc/mpc.go` 的 `result()`（以无匹配入边的节点为根沿 `matchLeft` 走，孤立点自成一条）/ `api/api_test.go` 的 `TestRandomCovers`、`mpc/mpc_test.go` 的 `TestSolveExampleSix`。
2. 覆盖最小（cover = n−|M|，|M| 为带回溯 Kuhn 最大匹配）：`mpc/mpc.go` 的 `augment`/`Solve` / `mpc/mpc_test.go` 的 `TestCoverMatchesBruteForce`。
3. 与朴素参照逐值一致（小规模独立枚举全部边子集求最大）：`mpc/mpc_test.go` 的 `bruteMaxMatch` 与 `TestCoverMatchesBruteForce`。
4. 失败不留痕（越界/自环/重复/n 非正，全部先校验后改状态）：`dag/dag.go` 的 `New`/`AddEdge` / `api/api_test.go` 的 `TestRejectLeavesStateUnchanged`、`TestSentinelErrors`。
