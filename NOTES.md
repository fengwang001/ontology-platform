# 圆与简单多边形关系判定 — 推导与不变量

## 六圆分步推导（P=[0,4]×[0,4]，边：底 y=0、右 x=4、顶 y=4、左 x=0）

| 圆 | 边界最近点（截断投影后） | D=平方距离 | 圆心在内 | r² | 关系 |
|---|---|---|---|---|---|
| C1 (2,2) r=1 | 四边中点如 (2,0) | 4 | 是 | 1 | CONTAINED（4>1 且在内） |
| C2 (2,2) r=3 | 同上 | 4 | 是 | 9 | CROSSING（4<9） |
| C3 (6,2) r=1 | (4,2) | 4 | 否 | 1 | DISJOINT（4>1 且在外） |
| C4 (5,2) r=1 | (4,2) | 1 | 否 | 1 | TANGENT（1==1） |
| C5 (2,5) r=1 | (2,4)（边内部，非顶点） | 1 | 否 | 1 | TANGENT（1==1） |
| C6 (5,-2) r=2 | (4,0)（投影 (5,0) 截断到端点） | 5 | 否 | 4 | DISJOINT（5>4） |

- (甲) C6 正确为 **DISJOINT**（D=5>4）。误用无限直线距离：到直线 y=0 得 d²=4、到直线 x=4 得 d²=1，D 错成 **1**，1<4 错判 **CROSSING**。
- (乙) C5 正确为 **TANGENT**（D=1，最近点 (2,4) 在顶边内部）。只算顶点距离：最近顶点 (0,4)/(4,4)，d²=2²+1²=**5**>1 且圆心在外，错判 **DISJOINT**——漏掉了边内部最近点。
- (丙) C4 正确为 **TANGENT**（D=1=r²）。无相切分支时 D==r² 落入"否则相离"，错判 **DISJOINT**。

## 四条不变量：代码位置 → 钉住它的测试

1. 与朴素遍历一致：`crel.Engine.Relation` 网格环形扩展+距离下界剪枝（crel/crel.go），对照 `crel.NaiveRelation` → `TestNaiveConsistency`
2. 截断投影精确距离：`cgeom.PointSegDist2` 投影参数截断 [0,1]、有理数平方距离全程整数（cgeom/cgeom.go）→ `TestPointSegDist2`、`TestSixCircles`
3. 四态互斥完备：`crel` 分类先判 `D==r²` 再判 `D<r²`（crel/crel.go classify）→ `TestFourStates`、`TestSixCircles`
4. 失败不留痕：`api.New` 全量校验通过后才替换实例、`api.Relation` 先校验再计算（api/api.go）→ `TestErrors`、`TestNoPartialResult`
