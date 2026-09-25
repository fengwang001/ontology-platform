# NOTES

## 第三节推导：按秩合并 + 路径压缩

- 朴素 Union 总把 y 的根挂到 x 的根下，树高可随链式 Union 线性增长，
  Find 退化为 O(n)（n=2^16 时单次 Find 跳数 >1000，见测试）。
- 按秩合并：rank 是树高的上界，矮树挂到高树下；仅当两树 rank 相等时
  新根 rank+1。归纳可证：rank 为 r 的根，其子树大小 ≥ 2^r
  （rank 只在两个大小均 ≥ 2^(r-1) 的树合并时从 r-1 升到 r，
  此时新子树大小 ≥ 2^(r-1)+2^(r-1)=2^r）。故 r ≤ log2(n)，
  即任意树高 ≤ log2(n)，n=2^16 时 ≤ 16。
- 路径压缩：Find 沿途把节点直接改指到根，之后这些节点的 Find 为 O(1)。
  压缩只降低树高、不改变 rank 上界性质，二者配合后 m 次操作均摊
  O(m·α(n))（Tarjan 界，α 为反 Ackermann 函数），实践中接近常数；
  单次 Find 跳数均摊界取 2·log2(n)+2 足以钉住回归。

## 第二节语义 → 代码/测试位置

- 连通性/Union 返回值：`uf/uf.go` 的 `Union`/`Connected`；测试 `TestVsNaive`
  （对照 `check/check.go` 的 `Naive`，经 `run` 助手逐对比较）。
- 分量计数：`uf/uf.go` 的 `Count`；`TestVsNaive` 末尾对比 `Naive.Count`。
- 传递性：`TestVsNaive` 首用例 `{0,1},{1,2}` 后验证 `Connected(0,2)`。
- ErrBadIndex：定义于 `uf/uf.go`，`id/id.go` 再导出；`TestBadIndex`
  用 `errors.Is` 覆盖 Find/Union/Connected 三处越界。
- 边界 New(0)/New(1)：`TestVsNaive` 的第 2、3 个用例。
- 跳数界：`TestFindHops`（链式 n=2^16 跳数 ≤16；内联 badUF >1000；
  随机 10000 次 Union 后任意 Find ≤ 2·log2(n)+2）。
- 并发：`uf.UF` 内嵌互斥锁；`TestConcurrentReads` 16 goroutine 只读，
  `go test -race` 干净。
