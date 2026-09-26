# NOTES

## 三、推导：s="aab", p="c*a*b"

`dp[i][j]` = 模式前缀 `p[0:j]` 是否整体匹配文本前缀 `s[0:i]`。递推：`p[j-1]=='*'` 时
`dp[i][j] = dp[i][j-2] || (match(s[i-1],p[j-2]) && dp[i-1][j])`；否则
`dp[i][j] = match(s[i-1],p[j-1]) && dp[i-1][j-1]`；`dp[0][0]=T`。

| i | s[i-1] | j=0 | j=1 `c` | j=2 `c*` | j=3 `c*a` | j=4 `c*a*` | j=5 `c*a*b` |
|---|--------|-----|---------|----------|-----------|------------|-------------|
| 0 | —      | T   | F       | T        | F         | T          | F           |
| 1 | a      | F   | F       | F        | T         | T          | F           |
| 2 | a      | F   | F       | F        | F         | T          | F           |
| 3 | b      | F   | F       | F        | F         | F          | T           |

`dp[3][5]=T`，故 `("aab","c*a*b")` 整体匹配为 true。

- (甲) `*` 错成「一个或多个」（`+`）：`c+` 要求文本至少以一个 `c` 开头，`"aab"` 没有 → 误判 **false**（正确 true）。
- (乙) `.` 错成「可匹配空串」：`"a."` 中 `.` 取零次，`"a"` 被整体覆盖 → 误判 **true**（正确 false）。
- (丙) 整体匹配错成「子串匹配」：`mis*is*p*.` 可匹配 `"mississippi"` 的子串 `"missi"`（m、i、`ss`、i、`s*`取空、`p*`取空、`.`=`s`）→ 误判 **true**（正确 false）。

## 二、四条不变量：保证位置与钉住它的测试

1. 与朴素一致：`match/match.go` 的 `matchTokens`（memo 版）与 `Naive` 共用同一递推与同一锚定（`k==len(toks)` 时返回 `i==len(s)`）；测试 `TestMatchConsistentWithNaive`（match 包，表驱动+随机循环逐对比对）。
2. 记忆化不改语义：`matchTokens(s,toks,useMemo)` 一个实现两种模式，memo 只缓存已确定的布尔结果；测试 `TestMemoSameAsNoMemo` 对同一输入逐对比较两种模式。
3. `.`/`*` 语义精确：`matchTokens` 中 `.` 仅在 `i<len(s)` 时消费恰好一个字符；`*` 只有「零次 `f(i,k+1)`」或「消费一个后 `f(i+1,k)`」两个分支；测试 `TestDotStarSemantics`（含空文本、`.` 不能为空、`*` 取零/多次）。
4. 失败不留痕：`parse.Parse` 先整体校验再返回，失败时不产生任何 Token；`api.Compile` 失败不建对象；`api.Matcher.Match` 先判超长再求值；三者均无共享可变状态；测试 `TestRejectLeavesStateUnchanged`（api 包，拒绝前后结果逐对相同）。
