# NOTES

## 推导：全负数时「非空子数组」的语义

Kadane 标准形式把当前和 clamp 到 0：`cur = max(cur + a[i], 0)`，
等价于允许取空子数组（和为 0）。全负数输入如 [-2,-3,-1] 时，
该写法每步都把 cur 重置为 0，最终返回 0 —— 即「空子数组」的和。

题目要求子数组非空，因此当前和应与「当前元素本身」比较：
`cur = max(cur + a[i], a[i])`。含义：要么把 a[i] 接进已有区间，
要么从 a[i] 重新开始一段，但绝不丢弃所有元素。全负数时
cur 始终是某个真实元素，结果为最大负数：[-2,-3,-1] 得 -1。

两种写法在全负数输入上的差异：
- 与 0 比较（错误）：返回 0，对应空子数组，违反业务语义。
- 与 a[i] 比较（正确）：返回 -1，即最大的那个负数。

测试钉住：`TestMaxSubarraySum` 中 [-2,-3,-1] 断言得 -1；
`TestClampToZeroIsWrong` 内联「与 0 比较」的错误实现并断言其返回 0。

## 语义条目落点（实现完成后补全）

1. 和最大：`kad/kad.go` MaxSubarraySum；测试 `TestMaxSubarraySum`（对照 `check.NaiveMaxSum`）。
2. 非空子数组：`kad/kad.go` MaxSubarrayRange 中 `cur+arr[i] < arr[i]` 重启；
   测试 `TestMaxSubarraySum`（全负用例）与 `TestClampToZeroIsWrong`。
3. 区间正确：`kad/kad.go` MaxSubarrayRange（hi 闭区间，首个最大者胜）；测试 `TestMaxSubarrayRange`。
4. 错误：`arr/arr.go` ErrEmpty/ErrNil/ErrTooLarge（`errors.Is` 可区分）；测试 `TestSentinelErrors`。
5. 边界：单元素、全正用例在 `sumCases` 表内，由 `TestMaxSubarraySum` 覆盖。
6. 线性访问：`kad/kad.go` 非导出计数器 accesses（atomic）；测试 `TestLinearAccesses`（n=100000，≤n）。
7. 并发纯函数：测试 `TestConcurrentPure`，`-race` 干净。
