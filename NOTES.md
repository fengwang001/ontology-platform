# 旋转排序数组二分查找推导

设区间为 `[lo, hi]`，`mid = lo + (hi-lo)/2`。循环右移后的严格升序数组在 `mid` 处切开时：

- 若 `nums[lo] <= nums[mid]`，左半边 `[lo,mid]` 严格有序（等号覆盖 `lo==mid`）。
  - target 满足 `nums[lo] <= target < nums[mid]` 时，答案只可能在左边，令 `hi=mid-1`。
  - 否则令 `lo=mid+1`，去跨越旋转点的右半边继续查找。
- 否则右半边 `[mid,hi]` 严格有序。
  - target 满足 `nums[mid] < target <= nums[hi]` 时，答案只可能在右边，令 `lo=mid+1`。
  - 否则令 `hi=mid-1`。

因此每轮先判定有序半边，再用该半边的两个端点围住 target；每轮区间至少减半，复杂度为 O(log n)。

反例：`[4,5,6,7,0,1,2]` 查找 `0`，中点是 `7`。普通二分只看到 `0 < 7` 就转向左边 `[4,5,6,7]`，但真正的 `0` 在跨越旋转点的右半边，所以必然漏查。

语义与测试映射：

- 命中、未命中：`rot/rot.go` 的 `Search`；由 `check/check_test.go` 的 `TestFixedCases`、`TestRandomMatchesLinear` 钉住。
- 空数组：`rot/rot.go` 的空区间直接返回 -1；由 `TestFixedCases` 与 `cmd/demo/main.go` 的 empty 判定钉住。
- 非严格旋转与三类哨兵：`arr/arr.go` 的 `ErrNilInput`、`ErrDuplicate`、`ErrInvalidRotate`；由 `TestValidation` 钉住。
- 单元素与无旋转：`Search` 的边界循环；由 `TestFixedCases`、`TestRandomMatchesLinear` 钉住。
- 漏查反例：`check/check_test.go` 的 `naiveBinary` 与 `TestNaiveBinaryMissesTarget`。
- 比较上界：`rot/rot.go` 的 `SearchCount`；由 `TestComparisonBound` 钉住。
- 并发纯函数：`check/check_test.go` 的 `TestConcurrentSearch`。
