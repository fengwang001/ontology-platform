# 推导与不变量

a="banana", b="ananas"（列 j=0..6，值为 dp[i][0..6]）

| i | a[i-1] | dp[i][0..6] | 本行max | 全局max |
|---|---|---|---|---|
| 1 | b | 0 0 0 0 0 0 0 | 0 | 0 |
| 2 | a | 0 1 1 0 1 0 0 | 1 | 1 |
| 3 | n | 0 0 2 2 0 2 1 | 2 | 2 |
| 4 | a | 0 1 1 0 3 0 0 | 3 | 3 |
| 5 | n | 0 0 2 2 0 4 1 | 4 | 4 |
| 6 | a | 0 1 1 0 3 0 5 | 5 | 5 |

全局最大值 5 位于 (i,j)=(6,5)，a 中起点 = 6-5 = 1，子串 a[1:6]="anana"。

- (甲) 误用子序列递推：abcde/abfce 公共子序列 "abce" 长度算成 4（正确 2；公共子串仅 "ab"、"e"）。
- (乙) 只读右下角：abcx/abc 的 dp[4][3]=0（末字符 x≠c 被清零），读成 0（正确 3）。
- (丙) 起点写成 bi-best-1（bi 为结尾的 1-based 位置 6）：6-5-1=0，错取 a[0:5]="banan"（正确 a[1:6]="anana"）。

## 不变量：保证位置 / 钉住测试

1. 与朴素一致：dp/dp.go `Solve` 滚动递推取全程最大值；dp/dp_test.go `TestSolveNaive`（表驱动+随机对拍）。
2. 子串真实：query/query.go `Run` 以 `a[start:start+len]` 切片；api/api_test.go `TestSubstringReal`（含 b 中确实出现与极大性）。
3. 滚动行=全表：dp/dp.go `Solve` 与 `FullTable` 同算法两种空间；dp/dp_test.go `TestRollingVsFullTable`。
4. 失败不留痕：api/api.go `New/Query/Substring` 全部先校验后执行、实例状态构造后只读；api/api_test.go `TestRejectedOpsNoTrace`。

空间计数器：dp/dp.go 非导出字段 `solver.cells`，仅同包测试读；dp/dp_test.go `TestRetainedCells` 断言恒为 min(n,m)+1。
