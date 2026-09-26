# NOTES

## 五步裁剪表（M=10^6；初始正方形 (-M,-M),(M,-M),(M,M),(-M,M)，逆时针）
| 步 | 半平面 | 区域顶点（逆时针） |
|---|---|---|
| 1 | H1 −x≤0（x≥0） | (0,−M),(M,−M),(M,M),(0,M) |
| 2 | H2 −y≤0（y≥0） | (0,0),(M,0),(M,M),(0,M) |
| 3 | H3 x+y≤6 | (0,0),(6,0),(0,6) |
| 4 | H4 x≤4 | (0,0),(4,0),(4,2),(0,6) |
| 5 | H5 y≤4 | (0,0),(4,0),(4,2),(2,4),(0,4) |

- (甲) 最终五顶点：(0,0),(4,0),(4,2),(2,4),(0,4)。H3 反成 x+y≥6 后错成三角形 (2,4),(4,2),(4,4)。
- (乙) H6 −x≤−5（x≥5）与五边形 x≤4 严格冲突 → 空集（无顶点）。若把“平行相反”误当“平行冗余”跳过，会错返裁剪前的五边形。
- (丙) 严格 < 会把边界交点 (6,0)、(0,6) 判在外而丢弃，第 3 步只剩 (0,0)，区域退化为空。

## 不变量落点（代码位置 / 钉住的测试）
1. 朴素一致：hpi.go 的 Add 按序做 SH 裁剪，Region 即逐次裁剪结果；SelfCheck 另用独立 naiveReplay 复核 — TestNaiveConsistency。
2. 可行性：hpi.go 全部顶点用 big.Rat 精确判定 Side≤0（含边界）；SelfCheck 逐顶点逐面 + 外点采样必有一面违反 — TestFeasibility。
3. 凸 / CCW / 无重复：CCW 初始盒、clip 保向、dedup 去重 — TestConvexCCW（五步序列由 TestFiveSteps 钉住）。
4. 失败不留痕：hpi.Add 在碰任何状态前完成全部校验（两类哨兵错误），api.Add 直接转发 — TestRejectedInputLeavesState。
另：O(1) 包围盒冗余预判 + edgesChecked 计数器由 TestRedundantEdgeCountBounded 钉住（同包直读字段，不经导出接口）；并发只读由 TestConcurrentReaders 钉住。
