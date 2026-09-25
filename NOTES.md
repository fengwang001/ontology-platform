# NOTES

## 层级提升的可复现推导

跳表插入时用几何分布决定节点层级（p=1/2 抛硬币，期望 O(log n) 层）。
问题：若用 `math/rand` 全局源（Go 自动随机播种）或不固定种子的源，
每次进程启动随机序列不同 → 同一批 key 两次构建的层级分配不同 →
逐层指针结构不同 → 结构序列化字节不同，且范围查询的遍历代价随之抖动。
推导结论：层级必须是「构建参数 + 插入顺序」的纯函数。两条可行路线：
1. 由 `New(seed)` 的 seed 派生一条伪随机流（本实现采用：`rand.NewPCG`），
   同一 seed + 同一插入序列 ⇒ 同一层级序列 ⇒ 结构逐字节相同；
   不同 seed ⇒ 流不同 ⇒ 结构几乎必然有差异。
2. 用 key 自身哈希决定层级：与插入顺序无关也可复现，但 Delete/重插
   语义下流方案更贴近经典跳表，故选 1。
注意：只有成功 Insert 才消耗随机流，失败（重复 key）不消耗，保证
「同一批成功插入」这一前提即可复现。

## 语义与测试位置

- 可复现：`skip/skip.go` `New`+`Insert`（seed 派生 PCG 流）；`TestReproducible`
- 有序 Range：`skip/skip.go` `Range`；`TestRangeVsRef`（对照 `check.Ref`）
- 增删查/Len：`skip/skip.go` `Insert`/`Find`/`Delete`；`TestOpsAndErrors`
- 哨兵错误：`skip/skip.go` `ErrBadRange`/`ErrDuplicate`/`ErrNotFound`，`errors.Is` 区分
- 边界（空表/删空）：`TestOpsAndErrors` 尾部断言
- 复杂度：`skip/skip.go` 非导出计数器 `cmp`；`TestComparesBound`（均值 ≤ 64）
- 并发只读：`TestConcurrentRead`（16 goroutine，`-race` 干净）
