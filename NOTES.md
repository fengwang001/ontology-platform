# ontology-760：后缀自动机 NOTES

## 第三节推导：s="abcbc" 的 SAM

逐步 extend：'a'→状态1；'b'→2；'c'→3；'b'→4 并分裂出克隆5；'c'→6 并分裂出克隆7。

| 状态 | len | link | len-len[link] |
|------|-----|------|---------------|
| 0(根) | 0 | - | 0 |
| 1 | 1 | 0 | 1 |
| 2 | 2 | 5 | 1 |
| 3 | 3 | 7 | 1 |
| 4 | 4 | 5 | 3 |
| 5 | 1 | 0 | 1 |
| 6 | 5 | 7 | 3 |
| 7 | 2 | 0 | 2 |

不同子串数 = 1+1+1+3+1+3+2 = 12。

- (甲) 忘减 len[link[v]]：1+2+3+4+1+5+2 = **18**（正确 12）。
- (乙) 终止状态只记整串后缀链 {6,7,0}："b" 转移到状态 5，其 link 子树 {5,2,4} 无终止状态 → **0**（正确 2）。
- (丙) 无转移直接重置 (根,0)：t="bcabc" 推进 b→1,c→2，遇 'a' 重置，再 b→1,c→2，最大 **2**（正确 3，"abc"）。

## 第二节四条不变量：保证位置与钉住测试

1. 与朴素一致：query/query.go 的 Distinct/Occurrences/LongestCommonSubstring 严格按定义实现；测试 TestQueriesMatchNaive（api/api_test.go）与 naiveDistinct/naiveCount/naiveLCS 对比。
2. link 树结构：sam/sam.go 的 extend 克隆+重定向保证 len[v]>len[link[v]]、无环、转移目标 len≥len[v]+1；测试 TestLinkTreeInvariants（sam/sam_test.go）。
3. 右端集合计数：query/query.go 的 New 给当过 cur 的状态记 1、按 len 降序沿 link 累加；不存在的子串沿 Next 缺失返回 0；测试 TestQueriesMatchNaive 含不存在子串断言。
4. 失败不留痕：api/api.go 的 New/Occurrences/LongestCommonSubstring 先校验再动状态，三类哨兵错误互不相同；测试 TestSentinelErrors、TestRejectedOpsKeepState（api/api_test.go）。
