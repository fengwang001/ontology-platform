# NOTES

推导：s="adceb", p="*a*b"（dp[i][j] = s[:i] 与 p[:j] 是否匹配；`*` 取 dp[i-1][j]||dp[i][j-1]，`?`/字符取左上角）

| i | s[i-1] | j0 "" | j1 "*" | j2 "*a" | j3 "*a*" | j4 "*a*b" |
|---|---|---|---|---|---|---|
| 0 | - | T | T | F | F | F |
| 1 | a | F | T | T | T | F |
| 2 | d | F | T | F | T | F |
| 3 | c | F | T | F | T | F |
| 4 | e | F | T | F | T | F |
| 5 | b | F | T | F | T | T |

dp[5][4]=T。(甲) 不回溯：`*` 吃满 "aab" 后 "ab" 无文本可配 → false（正确 true）。
(乙) `?` 可配空：a+空+b 配上 "ab" → true（正确 false）。
(丙) 漏吞尾：`*` 取空后剩余 "b" 未被吞掉 → false（正确 true）。

第二节四条不变量的保证位置与钉住测试：

1. 与朴素 DP 逐对一致：match/match.go 中 `Match` 与朴素 `naiveDP` 同语义；测试 `TestMatchEqualsNaiveDP`（表驱动 + 随机对）。
2. 贪婪+回溯不丢匹配：match/match.go `engine.match` 的 `starP`/`matchS` 回溯分支；测试 `TestBacktrackingFindsMatch`。
3. `?`/`*` 语义精确：rune 对齐比较（`?` 恰好配一个 rune），结尾 for 吞掉剩余 `*`；测试 `TestWildcardSemantics`。
4. 失败不留痕：parse/parse.go `Validate` 先整体校验；api/api.go `Compile` 失败不返回对象、`Match` 被拒前不触碰已编译状态；测试 `TestRejectionLeavesState`。

复杂度 O(n+m)：`engine.steps` 为非导出计数字段，`TestAdvanceCountIsLinear` 断言 steps ≤ 2(n+m)，计数器不经任何导出接口泄露。
并发：`Compiled` 构造后不可变，`TestConcurrentMatch` 钉住并发与串行逐对相同；`TestSelfCheck` 钉住自检通过。
