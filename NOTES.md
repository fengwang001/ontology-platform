# 几何哈希推导（T=正方形，基 a=(0,0),b=(2,0)；d=(2,0), perp(d)=(0,2), |d|²=4）

| T 点 | p−a | u=⟨p−a,d⟩/4 | v=⟨p−a,perp⟩/4 |
|---|---|---|---|
| (0,0) | (0,0) | 0 | 0 |
| (2,0) | (2,0) | 1 | 0 |
| (2,2) | (2,2) | 1 | 1 |
| (0,2) | (0,2) | 0 | 1 |

S 各点在对应基((5,5),(5,7))下算出同一组 (u,v)：(0,0),(1,0),(1,1),(0,1)，故 key 全同、匹配成立。
(甲) 镜像 (0,0),(2,0),(2,−2),(0,−2) 的 v 全部取反：(0,0),(1,0),(1,−1),(0,−1)；有序基+CCW perp 下 key 不再重合 → 不匹配。基改无序/颠倒方向等于允许反射，镜像会凑满 k−2 票 → 假阳性。
(乙) 旋转 30° 时 u,v 含 √3 等无理数，两次浮点结果末位不同，精确相等当 key 必然漏匹配（found=false）。正确：固定步长 δ=1/64 + math.Round 量化，误差远小于 δ 的同副本两侧落同一格。
(丙) 不设阈值时任意噪声点对碰巧同格即判匹配 → 随机场景大量假阳性。正确阈值 k−2（基占 2 点，其余每点一票，全局最大基才采纳）。

## 四条不变量（代码位置 / 钉住的测试）

1. 与穷举一致：match.go 的 Match 遍历全部有序基取全局最大、非基票达 k−2（总支持 k）；match_test.go: TestExhaustiveConsistency（200 组随机 T/S 与保向相似副本，同 gh.BruteForceContains 逐条对齐）。
2. 相似不变：gh.go BasisUV 用内积/|d|² 的规范坐标；api_test.go: TestSimilarityInvariant（T、S 同施整数相似变换，mapping 逐字段相同）。
3. 无反射：gh.go Perp 固定 (−y,x)、基有序；match.go 用带号 rV 的 int64 有理等式核验，镜像 rV 变号必失败；api_test.go: TestMirrorRejected（手性三角形镜像/反射再旋转，found=false）。
4. 失败不留痕：api.go NewTemplate/Match 先做全部校验再构造/投票，错误为 5 个互异哨兵；api_test.go: TestRejectionErrors、TestNoPartialResult（拒后 m=nil 且模板仍可正常 Match）。
复杂度计数（match.go Result.votes 非导出字段，=n(n−1)(k−2)）：match_test.go TestCounterQuadratic（n=100..10000 断言恰为该式且 ≤(k−2)n²）；并发只读（Engine 不可变）：api_test.go TestConcurrentMatch；自检：api.go SelfCheck / api_test.go TestSelfCheck。
