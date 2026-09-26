# NOTES — ontology-769
十行表：A=[0,3]×[0,3]，B=[2,5]×[2,5]；内=严格内部→丢弃，外→保留。
1 A(0,0) 对 B：外，保留
2 A(3,0) 对 B：外，保留
3 A(3,3) 对 B：内（2<3<5），丢弃
4 A(0,3) 对 B：外，保留
5 B(2,2) 对 A：内（0<2<3），丢弃
6 B(5,2) 对 A：外，保留
7 B(5,5) 对 A：外，保留
8 B(2,5) 对 A：外，保留
9 I1=(3,2)：A 边(3,0)-(3,3) × B 边(2,2)-(5,2)
10 I2=(2,3)：A 边(3,3)-(0,3) × B 边(2,5)-(2,2)
(甲) 并集八顶点 CCW：(0,0),(3,0),(3,2),(5,2),(5,5),(2,5),(2,3),(0,3)。凸包错成六顶点：(0,0),(3,0),(5,2),(5,5),(2,5),(0,3)。
(乙) 漏交点错成：(0,0),(3,0),(0,3),(5,2),(5,5),(2,5)（自交、凹口错位）；漏掉 I1=(3,2)、I2=(2,3)。
(丙) 点内判定符号写反：A 的 (3,3) 被错误保留、B 的 (2,2) 被错误保留。
不变量 保证位置 / 钉住的测试（函数均真实存在）：
1 面积守恒：un.IntersectionArea2（分割段中点归属+鞋带求和）与 api.checkPair 的 area(A)+area(B)-area(A∩B) 等式；un.TestRandomPairs、api.TestValidUnionAndArea
2 简单/无重复/CCW/面积非负/输入顶点非留即严格在内：un.UnionPoints 有向遍历+slices.Reverse、api.validate、api.checkPair 顶点循环；un.TestRandomPairs
3 覆盖双向（严格内于 A∨B ⇔ 严格内于结果）：api.checkPair 整数格点采样（跳过任一边界点）；api.TestValidUnionAndArea（调用 SelfCheck）
4 失败不留痕（四类哨兵互异，先校验后构造，拒收后仍可用）：api.NewPolygon/validate；api.TestRejectionNoPartial
