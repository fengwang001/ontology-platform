# Rotated Binary Search

设闭区间为 `[lo, hi]`、中点为 `mid`。
对无重复元素的循环右移升序数组，切口中至少有半边严格升序：
若 `nums[lo] <= nums[mid]`，则左半边 `[lo, mid]` 有序；否则右半边 `[mid, hi]` 有序。
因此先识别有序半边，再只在该半边内做普通区间判断：
左侧有序时，`nums[lo] <= target < nums[mid]` 才走左侧，否则走右侧；
右侧有序时，`nums[mid] < target <= nums[hi]` 才走右侧，否则走左侧。
不能只比较 `nums[mid]` 和 `target`：在 `[4,5,6,7,0,1,2]` 查找 `0`，
`nums[mid]=7 > 0`，普通二分只会向左；但 `0` 在旋转点右侧，因而漏查。
每轮先判半边有序性找旋转边界，再选择边界左侧或右侧的有序段普通二分；两阶段均为 `O(log n)`。
比较计数为闭包内局部变量；`Search` 无共享状态，可安全并发。

## 代码与测试

- `rot/rot.go`: `Search`、`SearchCount`、`searchWithCount`，实现查找与比较计数。
- `arr/arr.go`: `Search`、`Validate`，以及 `ErrEmpty`、`ErrDuplicate`、`ErrNotRotatedSorted`。
- `check/check.go`: `LinearSearch` 线性参照，以及测试用随机构造和并发辅助。
- `check/check_test.go`: `TestFixedCases` 钉住指定下标、未命中、空、单元素和有序数组。
- `check/check_test.go`: `TestNaiveBinarySearchMissesRotatedTarget` 内联错误二分并断言漏查。
- `check/check_test.go`: `TestRandomMatchesLinearSearch` 做 10000 组随机对拍。
- `check/check_test.go`: `TestComparisonBound` 断言 n=100000 的比较次数上界。
- `check/check_test.go`: `TestValidationSentinelsAndConcurrentSearch` 覆盖哨兵错误和并发。
