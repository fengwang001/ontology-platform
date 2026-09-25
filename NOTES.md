# NOTES

## 推导：分区后的递归方向（题目第三节）

Lomuto 分区把 arr[lo..hi] 重排为 `[<pivot] pivot [>=pivot]`，pivot 落在最终下标 p。
p 是 pivot 在排序后数组中的下标，k 也是排序后下标（0 起算），同属下标域，可直接比较：
`p == k` 返回 arr[p]；`p > k` 往左递归 [lo, p-1]；`p < k` 往右递归 [p+1, hi]。
每轮区间严格缩小（hi=p-1 或 lo=p+1），故必终止，平均 O(n)。

不能用 pivot 的"值"与 k 比较方向：k 是下标不是值，量纲不同。反例 `[3,2,1,5,4], k=2`
正确答案 3；按值比较会把值 2 误当目标而返回 2。`TestValueCompareIsWrong` 内联该错误
实现并断言其返回错误元素；`TestKthSmallest` 钉住 `[3,2,1,5,4],2→3`、`[3,2,3,1,2],3→3`。

## 语义与代码位置（题目第二节）

1. 第 k 小正确：`sel.KthSmallest`（sel/sel.go），`TestKthSmallest` 对照排序参照
   `check.KthSmallestRef`（check/check.go）。
2. 原地修改允许：`KthSmallest` 原地分区重排 arr；测试与 demo 传副本验证。
3. 哨兵错误：`ErrEmpty`/`ErrBadK` 定义于 sel/sel.go（sel 依赖为空），ord/ord.go
   别名再导出；`TestErrors` 用 `errors.Is` 区分。
4. 重复元素：`TestKthSmallest` 用例 `duplicates`、`all equal`。
5. 边界 k=0 / k=n-1：`TestKthSmallest` 用例 `min`、`max`。

## 复杂度与并发

- 非导出比较计数器 `atomic.Int64`（sel/sel.go），经 `Compares`/`ResetCompares` 访问；
  `TestCompareBound` 断言 n=100000 随机数组平均比较次数 <= 4n。
- `KthSmallest` 只改入参切片、计数器用 atomic，无共享可变状态；`TestConcurrent`
  在 `-race` 下并发验证。
