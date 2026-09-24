# NOTES

## 偶数中位数推导

| 步 | 喂入 | 排序后 | 两候选 | 结果 |
|---:|---:|---|---:|---:|
| 1 | -3 | [-3] | -3,-3 | -3 |
| 2 | -2 | [-3,-2] | -3,-2 | -3 |
| 3 | 5 | [-3,-2,5] | -2,-2 | -2 |
| 4 | -8 | [-8,-3,-2,5] | -3,-2 | -3 |
| 5 | 0 | [-8,-3,-2,0,5] | -2,-2 | -2 |
| 6 | -1 | [-8,-3,-2,-1,0,5] | -2,-1 | -2 |

- 规则：`a<=b` 时结果为 `floor((a+b)/2)`，即偶数长度向负无穷取整。
- 甲：第 2 步数学均值是 `-2.5`；选 `-3`。Go 截断式 `(-3+-2)/2=-2`，差异发生在候选和为负奇数时；负候选不应因向零截断而变大。
- 乙：若两边都写 `(a+b)/2`，第 2 步都错成 `-2`，两表达式彼此恒等，例如 `[-3,-2]`；改用同一个 `floorAvg` 后，对顶堆候选即排序序列的 `n/2-1`、`n/2`，故与朴素实现恒等。
- 溢出：`a+b` 在接近 `math.MaxInt64` 或 `math.MinInt64` 时可能溢出；写法为 `(a&b)+((a^b)>>1)`，有符号右移实现向负无穷取整且不求和。

## 不变量位置与测试

1. 逐步同朴素：`median.Median` 与 `median.floorAvg`；由 `TestNaiveMatchRandomAndRepeats`、`TestSixStepMedian` 钉住。
2. 容量差不超过 1：`median.rebalance` 在每次 `median.Push` 后执行；由 `TestHeapInvariantsAfterPush` 钉住。
3. 大顶堆顶不大于小顶堆顶：`median.Push` 的分流与 `median.rebalance`；由 `TestHeapInvariantsAfterPush` 钉住。
4. 失败不留痕：`median.Push` 与 `median.Median` 的前置哨兵错误、`api.Feed` 的整批容量预检；由 `TestSentinelErrorsAreStatePreserving` 钉住。
5. 比较次数为堆调整量级：`median.heap.cmp` 只在比较时增加，`heap2` 两堆比较均计入；由 `TestPushComparisonBound` 钉住。
6. 并发只读安全：`median.mu` 保护读路径；由 `TestConcurrentReaders` 钉住。
