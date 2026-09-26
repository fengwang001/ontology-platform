# NOTES（通配符匹配器）

s="adceb", p="*a*b"；列 j=0..4 对应模式前缀 "", "*", "*a", "*a*", "*a*b"。

| i | s[i-1] | dp[i][0..4] |
|---|---|---|
| 0 | - | T T F F F |
| 1 | a | F T T T F |
| 2 | d | F T F T F |
| 3 | c | F T F T F |
| 4 | e | F T F T F |
| 5 | b | F T F T T |

末格 T：`*`→ε、a→a、`*`→"dce"、b→b，整体匹配。

(甲) "*ab" on "aab"：* 尽量多吃且不回溯会吃光整串，末尾 a 无字符可配 → false（正确 true）。
(乙) "a?b" on "ab"：若 ? 可配空串，a→a、?→ε、b→b → true（正确 false）。
(丙) "a*" on "ab"：若漏掉尾部 * 吞余文，模式先耗尽而 "b" 残留 → false（正确 true）。

## 不变量：保证位置 / 钉住测试（函数均真实存在）

1. 与朴素 DP 逐对相同：match/match.go 的 `matcher.run` 双指针；match/match_test.go `TestNaiveEquivalence`（表驱动+随机对拍）。
2. 贪婪+回溯不丢匹配：match/match.go 的 `starPi/starSi` 回溯；`TestGreedyBacktrack`（*a*b、*ab、a*c?b 等）。
3. `?` 恰好一字符、`*` 含空串：match/match.go 的两个匹配分支与末尾吞星循环；`TestWildcardSemantics`。
4. 失败不留痕：api/api.go 的 `Compile` 先经 parse 全量校验再构造、Matcher 不可变；api/api_test.go `TestRejectLeavesState`。
三类可判定错误：parse/parse.go 的 `Validate`；api/api_test.go `TestCompileRejectKinds`。
线性计数器：match/match.go 未导出字段 `matcher.steps`；`TestComplexityLinear`（同包白盒直读）。
`SelfCheck`：match/match.go 与 api/api.go；api/api_test.go `TestSelfCheck`。并发：`TestConcurrentMatch`。
