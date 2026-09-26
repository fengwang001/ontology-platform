# NOTES — 增量 Delaunay（P1(0,0) P2(4,0) P3(0,4) P4(4,4) P5(3,3) P6(2,1)；三角形按 CCW 记）

分步表（外接圆判定 det：P4=0 共圆；P5 对 (P1,P2,P3) 为 96>0；P6 对 (P1,P2,P5)/(P3,P1,P5) >0，对另两圆 <0）：
1. 插 P1：无三角形
2. 插 P2：无三角形
3. 插 P3：(P1,P2,P3)
4. 插 P4：(P1,P2,P3),(P2,P4,P3)；内部对角线保留 P2P3（四点共圆不翻转，T0 不删，仅沿可见边 P2P3 补一个三角形）
5. 插 P5：(P1,P2,P5),(P2,P4,P5),(P4,P3,P5),(P3,P1,P5)（腔={T0,(P2,P4,P3)}，边界=凸包四边，扇形重连）
6. 插 P6：(P1,P2,P6),(P2,P5,P6),(P2,P4,P5),(P4,P3,P5),(P5,P3,P6),(P3,P1,P6)

(甲) 正确：保留对角线 P2P3，集合 (P1,P2,P3),(P2,P4,P3)。若用 >=0：共圆边 P2P3 被当非法边翻转，对角线错成 P1P4，集合错成 (P1,P2,P4),(P1,P4,P3)。
(乙) 正确集合见第5行。若只用点-三角形包含：只把包含三角形 (P2,P4,P3) 拆成 (P2,P4,P5),(P4,P3,P5),(P2,P3,P5)，而 (P1,P2,P3) 不重构；非法边 P2P3 两侧是 (P1,P2,P3) 与 (P2,P3,P5)（P5 在前者外接圆严格内部），应翻成 P1P5。
(丙) 正确集合见第6行。若拆完 (P1,P2,P5)→(P1,P2,P6),(P2,P5,P6),(P1,P6,P5) 不级联：非法边 P1P5 被保留（两侧 (P1,P6,P5) 与 (P3,P1,P5)，P6 在后者圆内），正确应翻成 P3P6，错误集合即上述三个新三角形加未动的 (P2,P4,P5),(P4,P3,P5),(P3,P1,P5)。

不变量在代码中的保证位置 / 钉住的测试：
1. 与暴力重算一致：tri.go `Mesh.Insert` 腔洞 BFS 只在 InCircle>0 时扩散（=0 不跨边）；`geo.InCircle` 用 math/big 精确行列式。测试：TestSixStepSequence、TestRandomTriangulation（独立参考实现逐面逐点验空圆）。
2. 三角剖分自洽（内部边恰两三角形、面数=2n-2-h、面积为正）：tri.go `Insert` 中 fan/rad 的邻接簿记与 `add` 保证边链接；自洽由 api.go `checkTriangles` 与 tri 测试 `validate` 独立核验。测试：TestEdgeIncidence、TestRandomTriangulation、TestHullCCW。
3. 局部 Delaunay（共圆不翻）：tri.go `Mesh.Insert` 严格 >0 判据与边界边扇形重连。测试：TestSixStepSequence（甲/乙/丙）、TestRandomTriangulation。
4. 失败不留痕：api.go `Service.Insert` 与 tri.go `Mesh.Insert` 全部校验（bounds/dup/collinear）在任何状态变更之前。测试：TestSentinelErrors（api）、tri 白盒 TestRejectedInsertLeavesState。
