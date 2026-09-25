# 旋转排序数组二分查找 · 推导与索引

## 三、推导：先判哪半边有序
旋转排序数组任取 mid，`[lo,mid]` 与 `[mid+1,hi]` 至少一半严格有序：
- 若 `nums[lo] <= nums[mid]`，旋转点必在右侧，左半 `[lo,mid]` 严格递增；
- 否则旋转点在左，右半 `[mid+1,hi]` 严格递增。

分支逻辑（每轮）：
1. 先用 `nums[lo] <= nums[mid]` 判定哪半有序（数组间比较）；
2. 再判 target 是否落在有序半的值域：左半 `[nums[lo],nums[mid]]`，右半 `(nums[mid],nums[hi]]`；
3. 落则缩到有序半，否则缩到另一半。`lo<hi` 时每轮区间必缩，故 O(log n)。

反例（不判半边、直接比 `nums[mid]` 与 target 的普通二分）：
`nums=[4,5,6,7,0,1,2], target=0`：mid=3 得 7>0 往左，mid=1 得 5>0 往左，
mid=0 得 4>0 再往左，区间空，返回 -1 —— 漏掉下标 4 的 0。原因：普通二分
假设全局有序，「target < nums[mid]」推不出「target 在左半」，必须先定向。

## 比较次数上界
只计关键比较（target 与数组元素之比）。每轮 ≤2 次（值域两端，等值用 `<=`
吸收进右端点），轮数 ≤ ceil(log2 n)，出循环 1 次终判，故 ≤ 2·log2(n)+4。

## 二、语义落点（代码位置 / 测试）
1. 命中：rot/rot.go `Search`；TestSearchTable、TestSearchVsLinear。
2. 未命中返回 -1：rot/rot.go `Search` 末尾；TestSearchTable。
3. 与线性参照一致：check/check.go `Linear`；TestSearchVsLinear（10000 组对拍）。
4. 空数组 -1 不报错：rot/rot.go（lo>hi 直出），TestSearchTable nil 用例；
   非严格旋转由 arr/arr.go `Validate` 拒绝（三类哨兵错误）；TestValidate。
5. 单元素 / 无旋转：TestSearchTable 对应用例。
复杂度上界：rot/rot.go 计数器 + `Comparisons`/`ResetComparisons`；TestComparisonBound。
并发：atomic 计数器，Search 无共享可变状态；TestConcurrent（-race 干净）。
