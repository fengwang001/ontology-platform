# NOTES

## 归并时逆序对贡献的时机

左右两半各自已升序。当 `L[i] > R[j]` 时，`L[i]` 是左半最小元，其后元素都
`>= L[i]`，故 `L[i..]` 全部严格大于 `R[j]`，且这些跨半对尚未被更浅层统计：
应加「左半剩余数」`mid-i+1`，然后取走 `R[j]`。相等时先取左半（`L[i] <= R[j]`），
归并才稳定，且相等不计逆序对。

错误口径——取右半时改加「右半已取走数」。例 [2,4,1,3,5]（`mid=(0+4)/2=2`，
左段 [2,4,1]、右段 [3,5]）：左段按错误口径得 0（漏掉 (2,1)(4,1)），
顶层 4>3 取 3 时右半已取走 0 个再加 0（漏掉 (4,3)），总计 **0**；
正确结果 **3**：(2,1)(4,1)(4,3)。该口径统计的是已结算过的右元素，既漏又错。

## 语义落点（第二节）

- 计数正确：`cnt/cnt.go` `CountInversions`；测试 `TestKnownCounts`、`TestMatchesNaive`。
- 稳定归并：`cnt/cnt.go` 中 `<=` 先取左半；测试 `TestStableMerge`。
- 溢出安全：计数与比较次数均 `int64`；测试 `TestComparisonBound`（n=100000，
  比较次数 ≤ n·log2(n)+n）。
- 空/单元素为 0、升序 0、降序 n(n-1)/2：`TestKnownCounts`。
- 哨兵错误：`arr/arr.go` `ErrNilSlice`/`ErrTooLong`/`ErrNegativeValue`；
  测试 `TestSentinelErrors`（`errors.Is`）。
- 并发纯函数：`TestConcurrentPure`；错误实现反例：`wrongCount` +
  `TestWrongImplIsActuallyWrong`（正确 3，错误口径 0）。
