# 回文树（Eertree）推导与不变量

## 第三节推导：s = "aabaa" 逐字符构建（奇根 len=-1，偶根 len=0）

| 步 | 当前字符(下标) | 本步新建回文(长度) | 该节点 link 指向 |
|---|---|---|---|
| 1 | a (0) | "a" (1) | 偶根 "" (len 0) |
| 2 | a (1) | "aa" (2) | "a" |
| 3 | b (2) | "b" (1) | 偶根 "" |
| 4 | a (3) | "aba" (3) | "a" |
| 5 | a (4) | "aabaa" (5) | "aa" |

共 7 个节点（含两根），不同回文 5 个：a, aa, b, aba, aabaa。
计数传播（从长到短沿 link 累加）：aabaa→aa，aba→a，aa→a，得 cnt(a)=4, cnt(aa)=2, cnt(b)=cnt(aba)=cnt(aabaa)=1，总和 9。

- (甲) 把「不同回文子串个数」错当「总个数」：会算成 **5**（不同回文数；正确的总回文数是 9）。
- (乙) 最长回文只扩奇数中心：对 "abba" 会算成 **1**（只剩单字符；漏掉偶长回文 "bb"/"abba"，正确是 4）。
- (丙) 出现次数不做 link 传播、只计节点新建次数：Count("a") 会算成 **1**（"a" 仅在第 1 步新建一次；正确是 4）。

## 第二节四条不变量：保证位置与钉住它的测试

1. 与朴素一致：`api.SelfCheck` 对内置串用 `naiveDistinct/naiveTotal/naiveLongest`（api.go）逐一比对；测试 `TestNaiveConsistency`（api/api_test.go）。
2. link 树结构：`pal.Build`/`add` 建链时保证 `len[link[v]] < len[v]`（故无环）、len 1 节点指向偶根；`pal.CheckLinks` 复核；测试 `TestLinkStructure`（pal/pal_test.go）。
3. 计数传播：`query.New` 按 len 降序把各节点 cnt 沿 link 累加；`api.Count` 先判回文再查；测试 `TestCountPropagation`（api/api_test.go）。
4. 失败不留痕：`api.New`/`Count` 先校验后动作，哨兵错误 `ErrEmpty/ErrTooLong/ErrNotPalindrome`；测试 `TestFaultIsolation`（api/api_test.go）。
