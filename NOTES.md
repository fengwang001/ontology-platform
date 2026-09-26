# NOTES

## 推导：m=8, k=3，h_j(x)=(j·x) mod 8，序列 Add(3) Add(5) Add(7) Remove(5)
1. Add(3)    命中 (3,6,1)  c=[0,1,0,1,0,0,1,0]
2. Add(5)    命中 (5,2,7)  c=[0,1,1,1,0,1,1,1]
3. Add(7)    命中 (7,6,5)  c=[0,1,1,1,0,2,2,2]
4. Remove(5) 命中 (5,2,7)，三槽全≥1 预检通过，各-1：c=[0,1,0,1,0,1,2,1]

- (甲) Query(5)=min(c5=1,c2=0,c7=1)=0，判**不存在**；错写成 max/任一≥1 会得 1，错判**存在（假阳性）**。
- (乙) 第二次 Remove(5)：c2=0，预检失败返回 ErrNotPresent，状态不变；不做下溢保护时 c2 会从 0 减到 **-1**。
- (丙) Remove(5) 前 Query(3)=min(c3=1,c6=2,c1=1)=**1**（min 在 c3 与 c1）；错用单哈希 c[h_2(3)]=c6=**2**，被 Add(7)（h_2(7)=14 mod 8=6 与 h_2(3)=6 碰撞）高估。

## 不变量：代码保证位置 / 钉住的测试函数
1. 无假阴性：cbf.Add 对 k 个槽全部 +1（cbf/cbf.go Add）→ TestNoFalseNegative
2. 精确移除：cbf.Remove 先确认 k 槽全≥1 再全部 -1（cbf/cbf.go Remove）→ TestExactRemoval
3. 朴素重放一致：计数器只做逐次 +1/-1 叠加，api.SelfCheck 内置序列与模型逐拍比对 → TestNaiveReplay、TestSelfCheck
4. 失败不留痕：api 构造校验 + cbf 先校验/预检后持锁写入（cbf/cbf.go Remove 预检在任何递减之前）→ TestRejectedOpsLeaveNoTrace
- 查询 O(k)：非导出 lastQueryTouches（atomic，cbf/cbf.go）→ TestQueryTouchesOnlyKCounters
- 并发：cbf 用 RWMutex，Query/SelfCheck 不碰彼此写状态 → TestConcurrentQueriesConsistent
