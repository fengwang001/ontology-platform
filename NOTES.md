# 旋转排序数组二分查找

## 推导：先判断哪半边有序

设严格升序数组被循环右移，枢轴为 k（断点在 nums[k-1] > nums[k]）。
取 mid 时，断点不可能同时落在 [lo,mid] 与 [mid,hi] 两边：

- nums[lo] < nums[mid] ⇒ [lo,mid] 不含断点，左半严格有序；
- 否则 nums[mid] < nums[hi]，右半严格有序（元素互不相同）。

于是每轮：先判定有序半边；target 落在该有序区间内则进入该半边，
否则进入另一半边。

反例：[4,5,6,7,0,1,2] 查 0。mid=7，朴素二分只看 7>0 就往左走，
而 0 在右侧，直接漏查。测试用 NaiveSearch 钉住该行为。

## 复杂度

比较次数按「每次循环 2 次元素探测：半边判定 + 命中判定」计，
区间包含判定是同一比较的复用、不计；循环 ≤ ceil(log2 n)+1，
故 2·(17+1) = 36 ≤ 2·log2(100000)+4 ≈ 37.3。

## 语义与代码位置

- 命中/未命中/空数组/单元素/无旋转：`rot/rot.go:Search`；测试 `TestSearch`
- 朴素漏查反例：`rot/rot.go:NaiveSearch`；断言在 `TestSearch`
- 对拍 10000 组：`check/check.go:LinearSearch`；测试 `TestDifferentialRandom`
- 比较上界：同测 `TestDifferentialRandom`，计数 `rot/rot.go:comparisons`
- 三类哨兵错误与校验：`arr/arr.go`；测试 `TestArr`
- 并发纯函数：`TestConcurrent`；demo：`cmd/demo/main.go`
