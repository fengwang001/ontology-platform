# 旋转排序数组二分查找

## 推导：先判哪半边有序

设 `mid=(lo+hi)/2`，数组由严格递增数组循环右移得到；旋转点至多一个，因此：
- `nums[lo] < nums[mid]` 时，左半 `[lo,mid]` 严格有序；
- 否则右半 `[mid,hi]` 严格有序（无旋转时左半同样有序，归入首条）。
- 左半有序：`nums[lo] <= target < nums[mid]` 则 `hi=mid-1`，否则 `lo=mid+1`；
- 右半有序：`nums[mid] < target <= nums[hi]` 则 `lo=mid+1`，否则 `hi=mid-1`。

每轮区间约半，故 O(log n)。

## 为什么直接比 `nums[mid]` 与 target 会漏

反例 `nums=[4,5,6,7,0,1,2]`、`target=0`：`mid=3` 处 `7>0`，朴素二分直接
`hi=2`，但 `0` 在下标 4 的右半边，永久漏查。根因：旋转后 `nums[mid]` 与
target 的大小关系不再决定方位，必须先知道哪半边单调才能裁剪区间。
测试 `TestNaiveBisectionMisses` 内联该错误实现并钉住漏查。

## 代码位置与测试

- `rot/search.go`：`Search`/`searchStats`，两段式（先 `⌈log₂n⌉` 定位旋转点，
  再在有序段普通二分）；`SearchWithCount` 暴露局部比较计数。
- `arr/arr.go`：`Validate`/`Search`；哨兵 `ErrNilInput`/`ErrDuplicate`/`ErrMalformed`。
- `check/check.go`：`LinearSearch` 参照；`check/check_test.go` 测试：
  `TestSearchTable`、`TestNaiveBisectionMisses`、`TestEquivalentToLinear`
  （10000 组随机对拍）、`TestComparisonBound`（n=100000，≤37，实测 35）、
  `TestConcurrentPure`（64 goroutine）、`TestArrErrors`（`errors.Is`）。
