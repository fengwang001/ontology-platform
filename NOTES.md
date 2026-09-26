# Boyer–Moore 推导与不变量

推导：text="abababcab"，P="abab"(m=4)，last: a=2,b=3，gs=[2,2,2,4,1]。

| s | 比较过程 | 坏字符 | 好后缀 | 前进 |
|---|---|---|---|---|
| 0 | j=3..0 全等，完全匹配 | — | gs[0]=2 | 2 |
| 2 | j=3..0 全等，完全匹配 | — | gs[0]=2 | 2 |
| 4 | j=3 失配：b≠text[7]=a | 3-last[a]=1 | gs[4]=1 | 1 |
| 5 | j=3,2 合；j=1 失配：b≠text[6]=c | 1-last[c]=2 | gs[2]=2 | 2 |

s=5 前进 2 后 s=7>n-m=5，结束，结果 [0,2]。
(甲) text="aaaaab",P="aaaab"：s=0 在 j=4 失配(b vs a)，错式 m-last[a]=2 跳到 s=2，漏掉唯一匹配 s=1（正确 [1]）。
(乙) text="aaaa",P="aa"：s=0 匹配后错跳 m=2，只得 [0,2]，漏掉 s=1（正确 [0,1,2]）。
(丙) text="ababab",P="abab"：s=0 匹配后 gs[0] 被当 m=4，s=4 越界，漏掉 s=2（正确 [0,2]）。

## 不变量保证位置与钉住测试

1. 与朴素一致：bm/bm.go 的 Search 循环；TestSearchNaive（表驱动+随机），SelfCheck 由 TestRejectedOpsLeaveNoTrace、TestConcurrentReaders 调用。
2. 坏字符跳跃安全：shift/shift.go Build 构造 last 表，bm/bm.go 的 bc 下限 1、取 max；TestBadCharTable + TestSearchNaive。
3. 好后缀跳跃安全：shift/shift.go goodSuffix（border 两遍法）与 bm/bm.go 取 max；TestGoodSuffixTable + TestSearchNaive。4. 失败不留痕：api/api.go New 先校验后建表、Search 先算局部结果再提交；TestRejectedOpsLeaveNoTrace。
