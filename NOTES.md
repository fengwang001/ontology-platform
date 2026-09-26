# NOTES

## a(b|c)* 的 Thompson 构造（状态按构造顺序，从 0 连续编号）
| 步 | 操作 | 新建状态 | 新增转移 |
|---|---|---|---|
| 1 | 字符 a | 0,1 | 0 -a-> 1 |
| 2 | 字符 b | 2,3 | 2 -b-> 3 |
| 3 | 字符 c | 4,5 | 4 -c-> 5 |
| 4 | 选择 b\|c | 6,7 | 6-ε->2, 6-ε->4, 3-ε->7, 5-ε->7 |
| 5 | 星 (b\|c)* | 8,9 | 8-ε->6, 8-ε->9, 7-ε->6, 7-ε->9 |
| 6 | 连接 a·(b\|c)* | 无 | 1-ε->8（星片段 start 是第 5 步新建的 8）；start=0，accept=9 |

(甲) `ab|cd` 解析为 `(ab)|(cd)`，**接受** "ab"；若令 `|` 优先级高于连接，解析成 `a(b|c)d`，则**拒绝** "ab"，只接受 `ad`、`bd`、`cd`。
(乙) `ab*` 解析为 `a(b*)`，**接受** "a"；若 `*` 错绑成 `(ab)*`，则 "a" 被**拒绝**（语言为 "" 或 `ab` 的整倍数）。
(丙) `(a|b)*` **接受**空串 ""；若 `*` 错实现成 `+`，空串被**拒绝**（要求至少一个 a/b）。

## 四条不变量：保证位置 / 钉住的测试
1. 与朴素回溯一致：`nfa/nfa.go` Compile 严格按 Thompson 规则；`api/api.go` SelfCheck 逐串交叉比对 `reast/reast.go` 的 ReferenceMatch。测试：TestNFAEqualsReference。
2. ε 闭包自洽：`nfa/nfa.go` closure 做传递闭包，After/Match 每个字符步后都重算闭包。测试：TestClosureIdempotent（逐步断言）；`api/api.go` SelfCheck 用包外独立 epsClose 对 After() 每步集合再核验。
3. 后缀语义：`reast/reast.go` Parse 把 Plus 展开成 X·StarX、Quest 展开成 Alt(X,Eps)；`nfa/nfa.go` build 的 Star 标准四 ε 边、Eps/Alt 照旧。测试：TestSuffixExpansion（与展开式逐串对比）。
4. 失败不留痕：`reast/reast.go` Parse 返回四个哨兵错误且不建 AST；`api/api.go` 无任何包级缓存，Compile 失败即返回 nil。测试：TestRejectErrorsDistinct、TestRejectionLeavesNoTrace。
复杂度计数器：`nfa/nfa.go` 非导出字段 touched，导出方法 `Append` 只读取/改动原 accept 一个状态（数值只能包内读取）。测试：TestAppendTouchesConstantStates。
并发：Match 全程只读 NFA。测试：TestConcurrentMatch。
