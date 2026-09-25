# NOTES：旋转排序数组的二分查找

## 三、推导：先判哪半边有序
不变式：旋转排序数组的任意区间 [lo,hi]，取 mid=(lo+hi)/2 后，[lo,mid]
与 [mid,hi] 至少有一半严格有序。若 nums[lo] <= nums[mid]，旋转点只可能
落在 (mid,hi]，故 [lo,mid] 严格有序；否则旋转点落在 (lo,mid]，故
[mid,hi] 严格有序。
分支逻辑：先判哪半边有序 → 再判 target 是否落在有序半的 [首,尾] 闭区间
内 → 在则收缩到有序半（hi=mid），否则收缩到另一半（lo=mid+1）。每轮
区间必缩（mid<hi 且 lo<mid+1），故 O(log n)。
反例：nums=[4,5,6,7,0,1,2]，target=0。不判半边的普通二分：mid=3 得
7>0 向左，mid=1 得 5>0 向左，mid=0 得 4>0 向左，返回 -1，漏掉下标 4。
原因：旋转后「比 target 小的元素」不再都位于 target 左侧，nums[mid] 与
target 的大小关系推不出 target 在哪半，必须先确定哪半边有序。
钉住：TestSearchTable（0→4、3→-1）、TestNaiveBinaryMisses（内联错误实现）。

## 四、计数约定与复杂度上界
rot 的非导出计数器只统计「关键字比较」（target 与数组元素的比较）；
nums[lo] 与 nums[mid] 的结构性比较不计入。每轮 ≤2 次关键字比较，轮数
≤ ceil(log2 n)，收尾 1 次 → 总计 ≤ 2·ceil(log2 n)+1；n=100000 时为
35 ≤ 2·log2(n)+4 ≈ 37.2。线性扫描最坏需 n 次，被该上界排除。

## 二、语义 → 代码 / 测试位置
（待测试写完后补充）
