# NOTES

## 为什么必须 1 起算（第三节推导）
Fenwick 树用 `lowbit(i) = i & -i` 决定下一个访问节点：
更新时 `i += lowbit(i)` 向上爬，查询时 `i -= lowbit(i)` 向下走。
若内部下标 0 起算，更新从 `i = 0` 开始，而 `lowbit(0) = 0 & -0 = 0`，
于是 `i += 0` 后 `i` 仍为 0，循环条件恒真，原地打转 → 死循环（线上事故根因）。
1 起算时任意 `i ≥ 1` 有 `lowbit(i) ≥ 1`，更新下标严格递增、越过 n 即退出；
查询下标严格递减、到 0 即退出，两种循环都必然终止。
结论：内部必须 1 起算；对外 API 保持 0 起算，进入 bit 包后统一 +1 平移。
测试钉住：`TestAgainstNaive`（n=1000、10000 次随机 Add 后前缀和全对，无死循环无越界）；
`TestZeroBasedDeadlock`（内联 0 起算不平移的错误实现，断言其 1000 步仍原地打转）。

## 语义与边界（第二节：代码位置 → 测试）
1. 前缀和：`bit/bit.go` `PrefixSum`，0..i 闭区间 → `TestAgainstNaive`
2. 区间和：`bit/bit.go` `RangeSum`，闭区间 [l,r] = PrefixSum(r)-PrefixSum(l-1) → `TestAgainstNaive`
3. 单点更新：`bit/bit.go` `Add` → `TestAgainstNaive`
4. 越界：`ErrBadIndex`（定义在 bit，`idx/idx.go` 重导出，errors.Is 可判）→ `TestErrorsAndEdges`
5. 边界：`New(0)` 合法；`PrefixSum(-1)` 返回 0（空前缀），i < -1 报 ErrBadIndex → `TestErrorsAndEdges`

## 复杂度（第四节）
`bit.BIT.visited` 为非导出计数器，记录单次操作访问节点数，经 `LastVisited` 读取。
n=100000 时每次 Add/PrefixSum 访问节点数 ≤ log2(n)+2 → `TestVisitBound`

## 并发（第五节）
`bit.BIT` 内嵌 `sync.Mutex`，写操作串行、读操作互斥进入；
16 个 goroutine 并发只读 PrefixSum 结果一致 → `TestConcurrentReads`（`go test -race` 干净）
