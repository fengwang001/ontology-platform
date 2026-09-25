# NOTES

## 层级提升的可复现推导

跳表插入时用随机层级（抛硬币的几何分布）维持期望 O(log n)。
若直接用 `math/rand` 包级函数（Go 1.20+ 全局源每次进程启动随机
自动播种），层级序列依赖进程启动状态，同一份数据两次构建得到
不同的层结构，`Range` 虽仍有序但结构不可复现，无法做字节级校验。
因此层级必须由「输入确定」的伪随机流产生：`New(seed)` 时用
`rand.New(rand.NewSource(int64(seed)))` 创建跳表私有源，每次成功
插入按固定顺序取随机数。同一 seed + 同一插入序列 => 同一随机流
=> 层级逐节点相同 => 结构逐字节相同；不同 seed => 随机流不同 =>
1000 个 key 下几乎必然存在层级差异（测试钉住）。注意只有插入
成功才消耗随机数（重复插入先返回 ErrDuplicate），保证随机流
与逻辑插入序列一一对应。

## 语义条目对照（第二节）

- 1 可复现：`skip.New` 私有随机源 + `List.Serialize`；`TestReproducible`
- 2 有序：`List.Range`；`TestSemantics`（对照 `check.Ref.Keys`）
- 3 增删查：`List.Find/Delete/Len`；`TestSemantics`
- 4 错误：`ErrBadRange`/`ErrDuplicate`（`errors.Is` 可区分）；
  `TestSemantics` 的 badrange/dups 用例
- 5 边界：空表 Range、删空 Len==0；`TestSemantics` 的 empty 用例
- 复杂度：非导出计数器 `cmps`（`List.Comparisons` 暴露读数）；
  `TestComplexity` 断言 10000 key 下 Find 平均比较次数 <= 64
- 并发只读：比较计数用 `atomic.Int64`，读路径无锁；
  `TestConcurrentRead`（16 goroutine，`-race` 干净）
