# Fenwick 树：为什么内部必须 1 起算

Fenwick 的更新与查询都靠 `lowbit(i) = i & -i` 定位下一个节点：

- 更新：`i += lowbit(i)`（向父节点）
- 查询：`i -= lowbit(i)`（向前缀块）

关键事实：对任何 `i`，`lowbit(i)` 取 `i` 最低位的 1，故 `i > 0 => lowbit(i) >= 1`。
而 `lowbit(0) = 0 & -0 = 0`。

若内部下标 0 起算且不做平移，从下标 0 更新时：

    i += lowbit(i)  // 0 += 0

`i` 永远等于 0，循环原地打转，无法终止，进程卡死（线上事故）。

结论：对外 API 保持 0 起算（符合直觉），内部统一 +1 平移为 1 起算：
`j := i + 1`，此后 `j >= 1`，`lowbit(j) >= 1`，更新严格递增、查询严格递减，
至多 `⌈log2 n⌉` 步结束。哨兵下标 j=0 只作为 `PrefixSum(-1)` 的自然终止点
（查询循环不进入，返回 0）。

## 语义条目与测试对照

- 前缀和（语义 1）：`bit/bit.go` PrefixSum；测试 TestMatchesNaive、TestSemantics。
- 闭区间和（语义 2）：`bit/bit.go` RangeSum（PrefixSum(r)-PrefixSum(l-1)）；TestMatchesNaive、TestSemantics。
- 单点更新（语义 3）：`bit/bit.go` Add；TestMatchesNaive（对朴素参照）。
- 越界错误（语义 4）：哨兵 ErrBadIndex/ErrBadSize/ErrBadRange（`bit/bit.go`，`idx/idx.go` 别名）；TestSemantics 用 errors.Is。
- PrefixSum(-1)=0、New(0)（语义 5）：`bit/bit.go`；TestSemantics 的 zero-size/prefix-minus-one。
- 0 起算死循环钉死（第三节）：TestZeroBasedSpins 内联 addZeroBased，超时 50ms 断言不返回。
- 复杂度 ≤ log2(n)+2：TestNodeBound（atomic 计数 LastNodes，n=100000）。
- 并发只读一致 + race：TestConcurrentReads（16 goroutine）；写操作用 sync.RWMutex 串行。
