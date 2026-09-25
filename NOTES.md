# NOTES - quickselect (第 k 小)

## 推导：分区后的递归方向（第三节）

partition 把当前区间重排为 `[<pivot] [pivot] [>pivot]`，pivot 落
在最终下标 p。此时 p 就是 pivot 在「整个区间排序后」的秩，因此：

- `p == k`：pivot 正是排序后下标 k 的元素，直接返回。
- `p > k`：下标 k 落在 `[lo, p-1]`，往左递归，右半不可能含答案。
- `p < k`：下标 k 落在 `[p+1, hi]`，往右递归。

不能用「pivot 的值」与 k 比较决定方向：k 是**下标**（0 起算的秩），
不是值。pivot 的值与 k 没有任何序关系——例如 `[3,2,1,5,4]` 求
k=2（答案 3），若按「pivot 值 > k 就往左」会把 4 与 2 比较而错误
地砍掉右半部分，最终返回 2。只有 pivot 的**位置** p 才与 k 同量纲。

## 语义条目 -> 代码 / 测试位置（第二节）

- 第 k 小正确：`sel/sel.go` `KthSmallest`；测试 `TestKthSmallest`
- 原地修改允许：分区即交换重排；测试 `TestKthSmallest`
- 哨兵错误：`sel/sel.go` `ErrBadK`/`ErrEmpty`；测试 `TestErrors`
- 重复元素确定：Lomuto 严格 `<` 分区；测试 `TestKthSmallest` 重复组
- 边界 k=0 / k=n-1：测试 `TestKthSmallest` 边界组
- 复杂度 <=4n 比较：`sel/sel.go` 原子计数器；测试 `TestComparisonBound`
- 并发安全：计数器用 `atomic`；测试 `TestConcurrent`
- 错误方向反例：测试 `TestValueCompareDirectionIsWrong`
