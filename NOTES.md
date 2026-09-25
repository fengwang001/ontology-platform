# Quickselect 设计笔记

## 分区后的递归方向（推导）

partition 把 arr[lo..hi] 重排为 `[<pivot] [pivot] [>pivot]`，pivot 落
在最终下标 p。p 的含义是：全数组中恰好有 p-lo 个区间元素比 pivot
小，即 pivot 就是「排序后下标为 p」的元素。k 也是下标（0 起算），
两者同属下标域，可直接比较：

- `p == k`：pivot 正是第 k 小，返回 arr[p]。
- `p > k`：第 k 小在左段 arr[lo..p-1]，令 hi = p-1 往左递归。
- `p < k`：第 k 小在右段 arr[p+1..hi]，令 lo = p+1 往右递归。

为什么不能用 pivot 的「值」与 k 比较：k 是下标不是值，值域与下标
域无关。例如 arr=[10,20,30]、k=1，pivot=10 的值 10 > k=1，若按值
判断会往左递归，但 p=0 < k，答案 20 其实在右段；按值判断直接漏掉
答案。只有下标 p 才携带「pivot 是第几小」的信息，值不携带。

## 语义与实现位置

- 第 k 小正确、允许原地重排：`sel.KthSmallest`（sel/sel.go:34），
  测试 `TestKthSmallest`（与 `check.KthRef` 排序参照一致）。
- 哨兵错误 ErrEmpty/ErrBadK（sel/sel.go:14，`errors.Is` 可区分，
  ord 包再导出）：`TestKthSmallest` 的 empty / k too large 用例。
- 重复元素、k=0 最小、k=n-1 最大：`TestKthSmallest` 对应用例。
- 平均比较次数 ≤4n：sel 非导出原子计数器（sel/sel.go:18），
  测试 `TestComparisonBound`（n=100000，10 次取平均）。
- 并发互不影响（纯函数，仅原子计数）：`TestConcurrent`，`-race` 干净。
- 「按值定方向」错误实现 `badKth`：`TestValueBasedDirectionIsWrong`
  断言其在 [10,20,30] k=1 上返回错误元素（正确值为 20）。
