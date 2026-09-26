# 最长公共子串推导笔记

## 三、分步推导：a="banana", b="ananas"

`dp[i][j]` = 以 `a[i-1]`、`b[j-1]` 结尾的最长公共后缀长度；字符相等则 `dp[i-1][j-1]+1`，否则 0。列 `j=0..6` 对应 `b="ananas"` 的前缀长度。

| i | a[i-1] | j=0 | 1 | 2 | 3 | 4 | 5 | 6 | 行最大 | 全局最大 |
|---|--------|-----|---|---|---|---|---|---|--------|----------|
| 1 | b      | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| 2 | a      | 0 | 1 | 0 | 1 | 0 | 1 | 0 | 1 | 1 |
| 3 | n      | 0 | 0 | 2 | 0 | 2 | 0 | 0 | 2 | 2 |
| 4 | a      | 0 | 1 | 0 | 3 | 0 | 3 | 0 | 3 | 3 |
| 5 | n      | 0 | 0 | 2 | 0 | 4 | 0 | 0 | 4 | 4 |
| 6 | a      | 0 | 1 | 0 | 3 | 0 | 5 | 0 | 5 | 5 |

答案：最大值 5 出现在 `i=6, j=5`，结束于 `a[5]`，起始下标 = 6-5 = 1，子串 `a[1:6] = "anana"`。

- **(甲)** 误用子序列递推：`a="abcde"`、`b="abfce"` 的最长公共子序列是 `"abce"`，会算成 **4**（正确为 2，子串 `"ab"`）。
- **(乙)** 只读右下角 `dp[n][m]`：`a="abcx"`、`b="abc"`，`a[3]='x'≠b[2]='c'`，`dp[4][3]=0`，会读成 **0**（正确为 3，最大值在 `dp[3][3]`）。
- **(丙)** 起始下标写成 `bi-best-1` = 6-5-1 = 0，会返回 `a[0:5]` = **"banan"**（正确 `"anana"`，起始 1）。

## 二、四条不变量：保证位置与钉住测试

1. **与朴素一致**：`dp.Core.Longest` 的滚动行递推与全扩展枚举等价（`dp/dp.go`）；测试 `TestQueryMatchesNaive`（`api/api_test.go`）。
2. **子串真实**：`query.Exec` 用 `a[start:start+length]` 取串（`query/query.go`），`api.Substring` 返回；测试 `TestKnownPairs`、`TestQueryMatchesNaive`（校验该段在 `b` 中同样出现）。
3. **滚动行正确**：`dp.Core.Longest` 单行滚动 + 对角暂存，空间 `O(min(n,m))`；测试 `TestRollingMatchesFullTable`（`dp/dp_test.go`）。
4. **失败不留痕**：`api.New`/`api.Query` 先校验再动手，拒绝路径不触碰任何字段（`api/api.go`）；测试 `TestErrorsAndStateIntact`（`api/api_test.go`）。
