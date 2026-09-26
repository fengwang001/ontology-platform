# NOTES

## 三、`a(b|c)*` 的 Thompson 分步构造（状态从 0 起连续编号）

| 步 | 操作 | 新建状态 | 新增转移 |
|---|---|---|---|
| 1 | 字符 `a` | 0,1 | 0 -a-> 1 |
| 2 | 字符 `b` | 2,3 | 2 -b-> 3 |
| 3 | 字符 `c` | 4,5 | 4 -c-> 5 |
| 4 | 选择 `b\|c` | 6,7 | 6ε->2, 6ε->4, 3ε->7, 5ε->7 |
| 5 | 星 `(b\|c)*` | 8,9 | 8ε->6, 8ε->9, 7ε->6, 7ε->9 |
| 6 | 连接 `a·(b\|c)*` | 无 | 1ε->8；start=0，accept=9 |

- **(甲)** `ab|cd` 解析为 `(ab)|(cd)`，**接受** `"ab"`。若 `|` 优先级高于连接，解析成 `a(b|c)d`，则**拒绝** `"ab"`，只接受 `"abd"`、`"acd"`。
- **(乙)** `ab*` 解析为 `a(b*)`，**接受** `"a"`（b 零次）。若误作 `(ab)*`，则**拒绝** `"a"`（语言为 `(ab)^n`）。
- **(丙)** `(a|b)*` **接受**空串 `""`（星的 8ε->9 旁路）。若 `*` 误实现成 `+`，则**拒绝**空串。

## 二、四条不变量的保证位置与钉住测试

1. **与朴素回溯一致**：`nfa/nfa.go` 的 `(*NFA).Match`（ε 闭包 + 字符推进）与 Thompson 构造共同决定语言；钉于 `api/api_test.go` 的 `TestMatchVsBacktracking`（表驱动 + 随机 pattern/串）。
2. **ε 闭包自洽**：`nfa/nfa.go` 的 `closure` 每步求传递闭包固定点；钉于 `nfa/nfa_test.go` 的 `TestEpsilonClosureInvariant`（逐字符步后集合再做闭包不再变化）。
3. **后缀语义**：`nfa/nfa.go` 的 `build` 中 Plus 直接构造成 `R·R*`、Quest 构造成 `R|ε`（ε 用空转移）；钉于 `api/api_test.go` 的 `TestSuffixSemantics`（与手工展开的 AST 对比语言）。
4. **失败不留痕**：`reast/reast.go` 的 `Parse` 为纯函数、`api/api.go` 的 `Compile` 先解析成功才构造，四类哨兵错误互不相同；钉于 `api/api_test.go` 的 `TestErrorsDistinctAndNoTrace`。

计数器为 `nfa.NFA.touched`（非导出），仅同包测试读取：`TestAppendTouchedConstant`；并发：`TestConcurrentMatch`（`go test -race`）。
