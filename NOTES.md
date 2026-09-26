# NOTES

## 第三节 KM 分步表（w = [[10,8,0],[10,0,0],[0,10,10]]，行=左 列=右）
1. 初始 l=[10,10,10] r=[0,0,0] M={}
2. 增广左0：紧边 0-0（10+0=10），M={0→0}
3. 增广左1：紧边只有 1-0(已占)，交替树 S={1,0},T={0}；slack j1=min(10+0-0=10←左1, 10+0-8=2←左0)=2，j2=min(10,10)=10；Δ=2
4. 调标：树内左减 Δ、树内右加 Δ → l=[8,8,10] r=[2,0,0]；树外 slack 同步减 2，j1 变 0
5. 新紧边 0-1，翻转增广路 左1-0-左0-1：M={0→1,1→0}，Σ=8+10=18
6. 增广左2：紧边 2-2（10+0=10）空闲直接匹配，M={0→1,1→0,2→2}，Σ=8+10+10=28（枚举 6 个排列的最大值，唯一最优）
- (甲) 贪心不回溯：0→0(10)，左1 在 j1/j2 的 0 中取小→1(0)，左2→2(10)，得总权 20、匹配 [0,1,2]；正确总权 28
- (乙) Δ 误把树外左节点也算入：左2 对 j1 有 10+0-10=0，第一次 Δ 错成 0（正确 2）；标号不变、无新紧边进入相等子图，增广永远失败，算法死循环
- (丙) 做成最小权：取排列 [2,1,0]，0+0+0=0（正确最大 28）

## 第二节四条不变量：代码位置 / 钉住的测试
1. 完美匹配：km.go augment 逐个左顶点在相等子图增广、mR 保证右顶点唯一；TestSolveMatchesBruteForce
2. 最大权：km.go 标号恒可行(l+r≥w)、delta 取树边最小 slack、互补松弛下增广；TestSolveMatchesBruteForce
3. 与朴素参照逐值一致：km_test.go brute 枚举全部 n! 排列取最大(含字典序)，与 Solve 逐值比；TestSolveMatchesBruteForce
4. 失败不留痕：bmg.go SetWeight/New 全部校验先于写入，api.go 四个互异哨兵错误；TestRejectedOpsLeaveNoTrace
- 字典序最小：km.go lexMin 行逆序 n-1..0 在相等子图做 Kuhn 增广、邻居按列号升序；TestLexicographicallySmallest
- delta O(1) 取最小 slack：km.go pq 索引最小堆+decrease-key+偏移量，非导出字段 lastDeltaChecks；TestDeltaRightChecksBounded
