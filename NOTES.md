# 旋转排序数组二分查找

- 令区间为 `[lo, hi]`、中点为 `mid`。循环右移后的数组由两段严格递增序列拼成。
- 若 `nums[lo] <= nums[mid]`，则 `[lo,mid]` 严格有序；否则旋转点必在其中，而 `[mid,hi]` 严格有序。
- 若左半边有序，只有 `nums[lo] <= target && target < nums[mid]` 时进入左半边，否则进入右半边。
- 若右半边有序，只有 `nums[mid] < target && target <= nums[hi]` 时进入右半边，否则进入左半边。
- 边界使用半开区间比较：有序左半边判断 `target < nums[mid]`，有序右半边判断 `nums[mid] < target`。
- 普通二分若只按 `target < nums[mid]` 选边：对 `[4,5,6,7,0,1,2]`、`target=0`，中点 7 会错误左移到 `[4,5,6]`，永久漏掉右半边的 0。
- 因此必须先识别哪半边仍保持有序，再判断 target 是否落在该有序区间；不能在不知道旋转点位置时直接按中点值选全局方向。
- 每轮执行常数次整数比较；排除半个区间，迭代次数为 `ceil(log2 n)`，总比较次数满足 `2*log2(n)+4` 的上界。

## 代码位置与测试

- 命中、未命中、空数组、单元素与无旋转：`rot/rot.go` 的 `Search`；测试 `TestSearchCases`。
- 10000 组随机旋转点和随机 target 对拍线性参照：`check/check_test.go` 的 `TestSearchMatchesLinearReference`。
- 普通二分不判半边的错误反例：`check/check_test.go` 的 `naiveBinarySearch` 与 `TestNaiveBinarySearchMissesRotatedTarget`。
- 比较次数上界：`rot/rot.go` 的 `SearchWithComparisons`；测试 `TestComparisonBound`。
- 三类哨兵错误与非法输入自洽：`arr/arr.go` 的 `Validate`、`Search`；测试 `TestArrValidation`。
- 并发 demo 判定位于 `cmd/demo/main.go`；竞态安全由 `go test -race` 验证。
