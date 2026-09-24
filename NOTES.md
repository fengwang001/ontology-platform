# NOTES

记 AB 表示可达对 (A,B)，E 为存在边[重数]。十步表（OD=过删数，RD=再推导数）：
1. E={AB}; R={AB} |R|=1; —
2. E={AB,BC}; R={AB,AC,BC} |R|=3; —
3. E={AB,BC,AC}; R={AB,AC,BC} |R|=3; —
4. E={AB,BC,AC,CA}; R={AA,AB,AC,BA,BB,BC,CA,CB,CC} |R|=9; —
5. E={AB,BC[2],AC,CA}; R 同第4步 |R|=9; —
6. E={AB,BC,AC,CA}; R 同第4步 |R|=9; OD=0 RD=0（重数仍>0）
7. E={BC,AC,CA}; R={AA,AC,BA,BC,CA,CC} |R|=6; OD=9 RD=6
8. E={BC,AC,CA,CD}; R={AA,AC,AD,BA,BC,BD,CA,CC,CD} |R|=9; —
9. E={BC,AC,CD}; R={AC,AD,BC,BD,CD} |R|=5; OD=9 RD=5
10. E={AC,CD}; R={AC,AD,CD} |R|=3; OD=2 RD=0
(甲) 第7步：OD=9（旧R全部9对：x∈{A,B,C}都到A，y∈{A,B,C}都被B到），RD=6，R见上。只过删不再推导：R=∅。只删AB本身：多出 BB、CB（B、C 经已删的 AB 才回到 B）。
(乙) 第9步：OD=9（x∈{A,B,C}到C，y∈{A,C,D}被A到，交集恰为旧R全部），RD=5，R见上。只删CA本身：多出 AA、BA、CC；自环对 AA、CC 因 A↔C 环被 CA 打断（A 出发只到 C,D 回不来，C 出发只到 D 回不来）而失效，BA 因 B→C 后再无路径到 A 失效。
(丙) 边当集合则第5步被忽略、第6步 BC 直接消失：该步 R={AA,AC,BA,BC,CA,CC}，比正确实现少 AB、BB、CB。正确实现第10步：OD=2（BC、BD），RD=0。

不变量（保证位置 / 钉住的测试）：
1. 与朴素BFS逐对一致：closure.AddEdge 并入集与 closure.RemoveEdge 的 DRed 按定义实现（closure/closure.go）；TestRandomAgainstBFS、TestTenSteps、api TestSelfCheck。
2. 重数语义：graph 仅在 mult 0↔1 跃迁时增删存在边（graph/graph.go）；TestMultiplicity。
3. 单向变化且减量=OD−RD：R 只在 D 上删除、只对 D 再加入，removeEdge 返回计数（closure/closure.go）；TestMonotoneDelta。
4. 失败不留痕：api 在改状态前完成全部参数/容量校验，哨兵错误（api/api.go）；TestSentinelErrors、TestRejectedOpsAtomic。
