# NOTES：三路分区推导与定位

## 指针不变量推导（荷兰国旗）
三指针：`lt` = 等于段起点，`gt` = 大于段起点，`i` = 当前扫描位置。
循环不变量：`[0,lt) < pivot`，`[lt,i) == pivot`，`[gt,n) > pivot`，`[i,gt) 未扫描`。
初始 `lt=i=0, gt=n`：三段皆空，未扫描段为全数组，不变量平凡成立。
- `arr[i] < pivot`：与 `arr[lt]` 交换（`[lt,i)` 全等于 pivot，换过来的必 == pivot 或即自身），`lt++, i++`，两段同时扩张一格。
- `arr[i] == pivot`：`i++`，等于段直接吞掉一格，无需交换。
- `arr[i] > pivot`：`gt--` 后交换 `arr[i]` 与 `arr[gt]`，大于段向左扩张一格；**i 不前移**，
  因为从 `arr[gt]` 换到 `i` 处的元素来自未扫描段，尚未分类，必须留在 `[i,gt)` 内下轮再判。
终止：`i == gt` 时未扫描段为空，三段即所求。每轮 `gt-i` 严格减 1，故恰 n 轮、每元素交换 ≤1 次，O(n)。

## 两路分区为何退化
两路只分「≤pivot | >pivot」，等于 pivot 的元素没有专属段。要挤出等于段，朴素做法是把
每个 == pivot 的元素逐个冒泡/挑选进等于区：第 k 个等于元素要穿过尚未分类的尾部，移动
次数正比于剩余长度，全等于 pivot 时总交换 ≈ n(n-1)/2，即 O(n^2)（quicksort 场景同理：
全相等时两路划分每次只切掉 pivot 一个元素，递归深度 n）。三路分区给等于段一个家，
扫描到 == pivot 时零交换直接跳过，故全相等输入交换 0 次。

## 语义条款 → 代码/测试定位
1. 三段正确：`part/part.go` ThreeWayPartition；测试 `TestThreeWayPartition`（区间对照 `check.Naive`，不变量过 `ord.Verify`）。
2. 多重集一致：`TestThreeWayPartition` 中排序后比较输入/输出。
3. 原地 O(1)：ThreeWayPartition 仅 lt/gt/i 三指针就地交换，无额外数组。
4. 空/单元素：`TestThreeWayPartition` 的 empty、single 用例（期望 (0,0)、(0,1)）。
5. 全小于/全等于/全大于：`TestThreeWayPartition` 的 all-lt、all-eq、all-gt 用例。
复杂度：交换计数器 `part.Swaps`，上界断言在 `TestComplexityBounds`；同测试内联 `twoWayBad` 证明两路退化（>n²/4 次交换）。
哨兵错误：`ord/ord.go` 的 ErrLowerSegment/ErrEqualSegment/ErrUpperSegment，`TestSentinelErrors` 用 errors.Is 区分。
并发：`TestConcurrent` 8 goroutine 并发分区，`-race` 干净；钉住用例 [2,0,2,1,1,0]→[0,0,1,1,2,2] 在 `TestThreeWayPartition/pinned`。
