# NOTES

## 推导：全负数时「非空子数组」的语义
Kadane 标准形式把当前和 clamp 到 0：`cur = max(cur + a[i], 0)`，等价于
允许空子数组——累计和变负就丢弃，从空区间重新开始。故全负数输入
（如 [-2,-3,-1]）上一路丢弃，最终返回空子数组的和 0。
题目要求非空，递推应改为 `cur = max(cur + a[i], a[i])`：「以 i 结尾的
非空子数组最大和」，要么接前面的最优区间，要么从 a[i] 重新开始，但绝不
能为空；答案取所有 cur 的最大值。全负数时退化为最大元素，[-2,-3,-1]
得 -1。两种写法差异：clamp 到 0 给空子数组和 0，与元素比较给最大负数
-1。测试同时钉住：正确实现得 -1，内联错误实现（与 0 比较）得 0。

## 语义与代码位置
1. 和最大：`kad/kad.go` MaxSubarraySum 对照 `check/check.go` NaiveMaxSum；
   测试 TestSumMatchesNaive。
2. 非空子数组：`cur = max(cur+a[i], a[i])`，全负返回最大负数；
   测试 TestNonEmptySemantics（含内联 clamp-to-0 错误实现断言为 0）。
3. 区间正确：MaxSubarrayRange 返回半开区间 [lo, hi)；测试 TestRangeConsistent。
4. 错误：`arr/arr.go` 哨兵 ErrNil/ErrEmpty/ErrTooLarge，errors.Is 可区分；
   测试 TestSentinelErrors。
5. 边界：单元素、全正取整个数组；测试 TestBoundaries。
6. 复杂度：kad 非导出计数器 accesses（atomic），n=1e5 访问 ≤ n；
   测试 TestLinearAccess。
7. 并发：MaxSubarraySum 纯函数，-race 干净；测试 TestConcurrentPure。
