# 算子延迟图与端到端瓶颈：推导与不变量

## 第三节推导

图：`S(0) A(30) A2(30) B(50) T(0)`；边 `S→A S→B A→A2 B→T A2→T`。`dist(v) = max(dist(u)+lat(v))`，源 `dist(S)=lat(S)`。

| 节点(拓扑序) | lat | dist | 达成 dist 的直接前驱 |
|---|---|---|---|
| S  | 0  | 0  | 无（源，dist=lat(S)） |
| A  | 30 | 30 | S（0+30） |
| B  | 50 | 50 | S（0+50） |
| A2 | 30 | 60 | A（30+30） |
| T  | 0  | 60 | A2（max(50+0, 60+0)） |

- **(甲)** `EndToEnd() = 60`（关键路径 S→A→A2→T）。错算成「所有算子 lat 直接求和」得 `0+30+30+50+0 = 110`（平行分支 S→B→T 被重复累加）。
- **(乙)** `Bottleneck() = A`，lat=30（关键路径上 A、A2 并列 30，取字典序最小者 A）。错算成「全局 lat 最大」会误判为 **B**（lat 50，但 B 不在关键路径上）。
- **(丙)** B 的 lat 改为 0：`dist(B)=0`，`dist(T)=max(0,60)=60`，`EndToEnd()` 仍为 **60**，`Bottleneck()` 仍为 **A(30)**，两者都不变。错把关键路径选成「经过全局最大延迟算子的路径」：在原图（B=50）会选 S→B→T，错报 `EndToEnd()=50`；B=0 时全局最大为 A/A2(30)，恰与真关键路径重合，仍报 60，错误被掩盖。

## 四条不变量（位置 + 钉住它的测试）

1. **与朴素参照一致**：`api` 包未导出 `brute()` 枚举全部源→汇路径求和取最大；`api.SelfCheck` 内置图逐张比对，测试 `TestEndToEndMatchesBrute` 钉住。
2. **瓶颈正确**：`crit.Bottleneck` 只在 `crit.CriticalPath` 重建出的路径上取最大 lat、并列取字典序最小；测试 `TestBottleneckTie`（含全局最大不在关键路径上的用例）与 `SelfCheck` 钉住。
3. **距离 DP 一致**：`dag.Graph.Longest` 按拓扑序单遍松弛，`dist(v)=max(dist(u)+lat(v))`、源 `dist=lat(源)`；测试 `TestDistDP` 与 `SelfCheck` 钉住。
4. **失败不留痕**：`dag.Add`/`dag.Link` 先完成全部校验（重名/负延迟/未知节点/环）再落写，拒绝时零修改；测试 `TestRejectedOpsLeaveState` 与 `SelfCheck` 钉住。
