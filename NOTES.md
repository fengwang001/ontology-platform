# NOTES

## 推导：s="aab", p="c*a*b"（dp[i][j] = s[:i] 整体匹配 p[:j]，T/F）

递推：j 位非 `*` 时 `dp[i][j]=dp[i-1][j-1] && ch`;p[j-1]=='*' 时 `dp[i][j]=dp[i][j-2] || (ch && dp[i-1][j])`,ch 为 p[j-1] 与 s[i-1] 的单字符匹配（`.` 匹配任意一个）。

| i | s[i-1] | j=0 | j=1(c) | j=2(c*) | j=3(c*a) | j=4(c*a*) | j=5(c*a*b) |
|---|--------|-----|--------|---------|----------|-----------|------------|
| 0 | —      | T   | F      | T       | F        | T         | F          |
| 1 | a      | F   | F      | F       | T        | T         | F          |
| 2 | a      | F   | F      | F       | F        | T         | F          |
| 3 | b      | F   | F      | F       | F        | F         | T          |

dp[3][5]=T → 整体匹配 true。

- (甲) `*` 错成「一个或多个」：`c*` 至少要一个 `c`，"aab" 没有 `c` → 误判 **false**（正确 true）。
- (乙) `.` 错成「可匹配空串」：`a.` 中 `.` 取空即覆盖 "a" → 误判 **true**（正确 false）。
- (丙) 错成「子串匹配」：`mis*is*p*.` 可取 s*="ss"、p*="pp"、`.`="i" 命中子串 "mississippi" → 误判 **true**（正确 false）。

## 四条不变量的保证位置与钉住它们的测试

1. 与朴素一致：`match/match.go` 的 `Naive`（无记忆化回溯）作对照 oracle；测试 `TestNaiveConsistency`（api_test.go，随机小输入逐对比对）。
2. 记忆化不改语义：`match/match.go` 的 `solve` 只在算完后写 `memo`，递推式与 `Naive` 逐行同构；测试 `TestMemoizedEqualsNaive`（match_test.go，枚举小模式/文本全比对）。
3. `.` 与 `*` 语义精确：`solve` 中 `first` 要求 `i<len(s)` 且单字符匹配，`*` 分支为零次(`j+2`)或多次(`i+1,j`)；测试 `TestDotStarSemantics`（api_test.go 表驱动，含 ("a","a.")=false、("","a*")=true 等）。
4. 失败不留痕：`api/api.go` 的 `Compile`/`Match` 先校验后动作，拒绝路径不写任何状态（memo 本就为每次调用私有）；测试 `TestFailureNoTrace`（api_test.go：三类错误各自可判定且互不相同，拒绝后旧 Matcher 结果不变）。
