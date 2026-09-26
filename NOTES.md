# NOTES

推导（P=[0,4]²，顶点逆时针；D=圆心到边界最短平方距离，判定先比 D==r²）

| 圆 | D 的来源 | D | 心在内 | r² | 关系 |
|---|---|---|---|---|---|
| C1 (2,2,r=1) | 到四边距离均为 2 | 4 | 是 | 1 | CONTAINED |
| C2 (2,2,r=3) | 同上 | 4 | 是 | 9 | CROSSING |
| C3 (6,2,r=1) | 最近点 (4,2) | 4 | 否 | 1 | DISJOINT |
| C4 (5,2,r=1) | 最近点 (4,2) | 1 | 否 | 1 | TANGENT |
| C5 (2,5,r=1) | 垂足 (2,4) 在顶边内部 | 1 | 否 | 1 | TANGENT |
| C6 (5,-2,r=2) | 投影 (5,0) 在底边延长线上，截断后最近顶点 (4,0) | 5 | 否 | 4 | DISJOINT |

(甲) C6 正确为 DISJOINT（5>4，心在外）。误用无限直线距离（不截断投影）：到直线 y=0 距离²=4，D 错成 4=r²，错判 TANGENT。
(乙) C5 正确为 TANGENT（边内垂足 (2,4)，D=1=r²）。只查顶点：最近顶点 (0,4)/(4,4) 距离²均为 5，漏掉边内最近点，D 错成 5>1，错判 DISJOINT。
(丙) C4 正确为 TANGENT（D=1=r²）。没有相切分支、把 D==r² 归入相离，则错判 DISJOINT。

不变量的代码保证位置 / 钉住的测试：

1. 与朴素遍历一致：crel.go 网格分环扩张只决定算哪些边，每边仍用 cgeom.PointSegDist2 同式取截断平方距离，gap2 下界保证停搜不丢最近边 → TestRelationMatchesNaive（随机圆心比对朴素 O(n)）、TestGridNoFullScan（各档 m 下 grid D==naive D）、TestRingsFindDistantEdge。
2. 距离精确（截断 [0,1]、整数平方无浮点）：cgeom.go PointSegDist2 按 dot≤0 / dot≥len² 取端点，否则距离为 cross²/len² 有理数，比较用 128 位交叉相乘 → TestPointSegDist2Clamped、TestSixCircles。
3. 四态互斥完备、相等优先：crel.go Classify 先 Equal 再 Less 再按内外二分 → TestSixCircles、TestFourStatesCoverage。
4. 失败不留痕：api.go 全部校验通过后才构造 Engine，被拒输入不触碰实例；拒绝后旧实例与新构造均正常 → TestRejectNoPartialResult。
网格不全表扫描：crel.go 按 cell 分桶、环扩并以环上最小点-格距为停止界；非导出计数器 checked，白盒 TestGridNoFullScan 断言 m=100…10000 各档计数 ≤16 且不随 m 增长。
并发只读：构造后不可变、checked 为 atomic.Int64，每查询去重集为局部变量 → TestConcurrentRelation（go test -race）。
