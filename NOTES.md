# 流式中位数：偶数长度合成规则推导

逐步喂入 `-3 -2 5 -8 0 -1`（lo 为大顶堆存下半，hi 为小顶堆存上半；规则：偶数取 (a+b)/2，Go 向零截断）：

| 步 | 喂入 | 排序后 | 两候选 lo顶/hi顶 | 结果 |
|---|---|---|---|---|
| 1 | -3 | [-3] | -3 | -3 |
| 2 | -2 | [-3,-2] | -3 / -2 | (-3+-2)/2 = -5/2 = **-2** |
| 3 | 5 | [-3,-2,5] | -2 | -2 |
| 4 | -8 | [-8,-3,-2,5] | -3 / -2 | -5/2 = **-2** |
| 5 | 0 | [-8,-3,-2,0,5] | -2 | -2 |
| 6 | -1 | [-8,-3,-2,-1,0,5] | -2 / -1 | -3/2 = **-1** |

(甲) 规则：偶数取 `(lo.Peek()+hi.Peek())/2`，采用 Go 整数除法（**向零截断**）。步 2 得 -2；而数学中位数 -2.5 向下取整（floor）得 -3。两者仅在「两候选之和为负奇数」时不同；选向零截断，因为 Go 的 `/` 与简单的堆顶/朴素表达式天然一致、正负对称，无需额外分支。

(乙) 对顶堆 `(lo.Peek()+hi.Peek())/2` 与朴素 `(sorted[n/2-1]+sorted[n/2])/2` 在该规则下**恒等**：lo 顶恰为 sorted[n/2-1]、hi 顶恰为 sorted[n/2]，入参与运算符完全相同（全负输入亦然，如 [-5,-1] 两式都得 -3）。溢出：a、b 接近 MaxInt64 时 `a+b` 溢出回绕。规避：相加前判定 `(b>0 && a>MaxInt64-b) || (b<0 && a<MinInt64-b)`，命中则返回 ErrOverflow，否则才相加。

## 不变量与位置 / 测试钉住

1. 逐步与朴素排序一致：`median.go` Median + rebalance 保证堆顶即中位候选；测试 `TestMatchesNaive`（含负/重复）与 `api.SelfCheck` 钉住。
2. 两堆个数差 ≤ 1：`median.go` rebalance（Push 末尾）保证；测试 `TestHeapBalance`、`api.SelfCheck` 钉住。
3. 大顶堆顶 ≤ 小顶堆顶：`median.go` Push 的先过顶再落堆 + rebalance 保证；测试 `TestHeapOrdering`、`api.SelfCheck` 钉住。
4. 失败不留痕：`api.go` Feed 先整体预检再写入；`median.go` safeAvg 先判定；测试 `TestErrorsNoStateChange`、`api.SelfCheck` 钉住。
