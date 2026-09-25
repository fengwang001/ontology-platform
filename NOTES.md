# NOTES：三路分区（荷兰国旗问题）

## 指针不变量推导
- 三指针：`lt`＝等于段起点，`gt`＝大于段起点，`i`＝当前扫描位置，初始 `lt=0, i=0, gt=len(arr)`。
- 循环不变量：`[0,lt)` 全 `<pivot`；`[lt,i)` 全 `==pivot`；`[gt,n)` 全 `>pivot`；`[i,gt)` 未扫描。终止条件 `i==gt`，未扫描段为空，三段即所求。
- `arr[i] < pivot`：与 `arr[lt]` 交换。因 `[lt,i)` 全等于 pivot，换到 `i` 处的元素必为 pivot（或 `lt==i` 自换），故交换后 `lt++、i++` 同时成立。
- `arr[i] == pivot`：直接 `i++`，等于段右扩一格，不做任何交换。
- `arr[i] > pivot`：先 `gt--` 再与 `arr[gt]` 交换。换到 `i` 处的元素来自未扫描段，性质未知，**`i` 不前移**，下轮重新判定；这是不变量得以维持的关键。
- 每轮要么 `i` 右移要么 `gt` 左移，未扫描段严格缩小，故每元素至多被交换一次，总交换次数不超过 n，时间 O(n)、额外空间 O(1)。
- 两路分区为何退化：只分 `<=pivot | >pivot` 时，等于 pivot 的元素全部落入左段；快速排序场景下全等输入每次划分出 `n-1 | 0` 的极不平衡两段，递归 n 层、每层 O(n) 次交换，累计 O(n^2)。三路分区把等于段一次性归位并排除出后续处理，杜绝重复交换。

## 语义条目 -> 代码/测试位置
- 三段正确：`part/part.go` `ThreeWayPartition`；测试 `TestConsistentWithNaive`（对照 `check.Naive` + `ord.Verify`）。
- 多重集一致：`ord.SameMultiset`，经 `check.Consistent` 由 `TestConsistentWithNaive` 覆盖。
- 原地 O(1)：`ThreeWayPartition` 只做指针移动与元素交换，无切片分配。
- 空/单元素：`TestConsistentWithNaive` 用例 `empty`、`single-*`；demo 判定 `empty`、`single-equal`。
- 全小于/全等于/全大于：`TestConsistentWithNaive` 用例 `all-less`、`all-equal`、`all-greater`。
- 钉住 [2,0,2,1,1,0]->[0,0,1,1,2,2]：`TestConsistentWithNaive` 尾部断言；demo 判定 `pinned-order`。
- 两路退化证明：`TestSwapCounts` 后半（内联 `twoWaySort`，全等 10000 元素交换次数 > 100n）。
- 哨兵错误：`ord.ErrBadRange/ErrWrongSegment/ErrLostElements`，测试 `TestSentinelErrors` 用 `errors.Is` 区分。
- 并发安全：`TestConcurrent`（`-race` 干净）；计数器为 atomic，结果不共享状态。

## 复杂度约束
- `part` 内非导出计数器 `swaps`（atomic），全等 n=10000 输入交换次数 `<=n`，由 `TestSwapCounts` 断言。
