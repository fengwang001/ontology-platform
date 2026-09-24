# NOTES

## 推导（S(0) A(30) A2(30) B(50) T(0)，边 S→A S→B A→A2 B→T A2→T，拓扑序 S,A,B,A2,T）

| 节点 | lat | dist（源→该节点最长，含自身） | 达成前驱 |
|---|---|---|---|
| S  | 0  | 0  | —（源） |
| A  | 30 | 30 = 0+30 | S |
| B  | 50 | 50 = 0+50 | S |
| A2 | 30 | 60 = dist(A)+30 | A |
| T  | 0  | 60 = max(dist(A2)+0, dist(B)+0) | A2 |

- (甲) EndToEnd=**60**（路径 S-A-A2-T=0+30+30+0；S-B-T 仅 50）。错算成「全部算子 lat 求和」=0+30+30+50+0=**110**。
- (乙) Bottleneck=**A，lat=30**（关键路径 S-A-A2-T 上最大 30；A 与 A2 并列取字典序小者 A）。错取「全局 lat 最大」→ **B(50)**，而 B 不在关键路径上。
- (丙) B 的 50 改成 0：EndToEnd **仍 60**、Bottleneck **仍 A(30)**，均不变（S-B-T 降为 0）。错判「关键路径=经过全局最大算子的路径」在 B=0 后会报 **60**（此时全局最大 A/A2 恰在真关键路径，纯属巧合）；在原数据下它只报 **50**，恰好暴露该判据错误。

## 第二节四条不变量：在哪里保证、被谁钉住

1. 与朴素参照一致：DP 在 `dag.Graph.Solve`（dag/dag.go）按拓扑序单遍求最长路；朴素枚举在 `crit.BruteEndToEnd`（crit/crit.go，DFS 枚举全部源→汇路径求和取 max）；`Pipeline.SelfCheck`（api/api.go）逐图比对。测试 `TestEndToEndAndBottleneck` 钉住。
2. 瓶颈正确：`crit.Analyze`（crit/crit.go）用 fwd+back 距离 fwd[v]+back[v]-lat[v]==E 判定关键路径成员，再取 lat 最大、并列取名字典序最小；测试 `TestEndToEndAndBottleneck`（含并列字典序与「B 不在关键路径」）钉住。
3. 距离 DP 一致：`dag.Graph.Solve` 的松弛 `dist[w]=max(dist[w],dist[v]+lat[w])`，源 dist=lat(源)，非导出 `relaxCount` 记考察边数（dag/dag.go）；独立重算在 `crit.IndependentDist`，由 SelfCheck 比对。测试 `TestDistDP` 与 `TestRelaxCountChain`（链 m=100/1000/10000，松弛恰 m−1）钉住。
4. 失败不留痕：`dag.Add`/`dag.Link` 全部校验通过后才写 map（dag/dag.go），源汇数目错误由只读 `Solve` 返回不触碰状态；`checkRejection`（api/api.go）四类拒绝后继续在同一图上正常使用。测试 `TestErrors_StatePreserved` 与 `TestBadTopologyAndSelfCheck` 钉住。
