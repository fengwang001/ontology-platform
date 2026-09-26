# Boyer–Moore 推导与不变量

## 分步表：text="abababcab"，P="abab"（m=4；last: a=2,b=3,余 -1；gs[0..4]=[2,2,2,4,1]）

| s | 比较过程（从右向左） | 坏字符跳跃 | 好后缀跳跃 | 前进 |
|---|----------------------|-----------|-----------|------|
| 0 | j=3..0 全匹配，命中 0 | —（全匹配不查表） | gs[0]=2 | 2 |
| 2 | j=3..0 全匹配，命中 2 | — | gs[0]=2 | 2 |
| 4 | j=3 失配：P[3]='b'≠text[7]='a' | 3-last['a']=1 | gs[4]=1 | 1 |
| 5 | j=3,2 匹配，j=1 失配：P[1]='b'≠text[6]='c' | 1-last['c']=2 | gs[2]=2 | 2 |

s=7 越界结束，结果 [0,2]，与朴素一致。

(甲) text="aaaaab"、P="aaaab"（m=5，last['a']=3）：s=0 时 j=4 失配于 text[4]='a'，错式
m-last['a']=5-3=2 直接跳到 s=2 并越界结束，越过唯一匹配 → 漏掉下标 1（正确 [1]）。
(乙) text="aaaa"、P="aa"：gs[0]=最小周期=1；错跳 m=2 得 s=0,2，结果 [0,2] → 漏掉下标 1。
(丙) text="ababab"、P="abab"：gs 恒为 m=4，s=0 全匹配后跳到 s=4 越界 → 漏掉下标 2（正确 [0,2]）。

## 四条不变量：代码位置 → 钉住它的测试

1. 与朴素一致：bm/bm.go `Searcher.Search` 主循环只取安全跳跃 → `TestSearchMatchesNaive`、`TestExhaustiveSmall`（api/api_test.go）。
2. 坏字符跳跃安全：`last` 取最右出现（shift/shift.go `BadChar`），跳跃 `max(j-last[c], gs[j+1])`（bm/bm.go）→ `TestSearchMatchesNaive` 随机档 + `SelfCheck` 内置表核验（`TestSelfCheck`）。
3. 好后缀跳跃安全：gs 按「最小安全跳跃」定义计算（shift/shift.go `GoodSuffix`），api 内 `bruteGS` 独立暴力核验（api/api.go `SelfCheck`）→ `TestSelfCheck`。
4. 失败不留痕：`New` 先校验后建对象、`Search` 算完才提交状态（api/api.go）→ `TestFailureLeavesNoState`。
