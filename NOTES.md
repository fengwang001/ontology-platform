# NOTES

## 层级提升的可复现推导

跳表每个节点的层级决定索引结构；结构不同则「同数据两次构建
逐字节相同」无法满足。常见做法用 `math/rand` 全局源抛硬币：全局
源状态受进程内全部调用影响，调度与调用次数差异都会改变层级序列，
结构因此不可复现；即使 `rand.New` 带固定种子，层级仍依赖插入
顺序，删插交替后同样漂移。

推导结论：层级必须是 `(seed, key)` 的纯函数，与插入顺序、进程
状态无关。实现见 `skip/skip.go` 的 `levelOf`：splitmix64 风格
混合哈希 `h = mix(key^seed)`，取末尾连续 0 位数为几何分布层级
（p=1/2，上限 `node.MaxLevel`=32）。同 seed 同 key 集 => 各节点
层级相同 => 结构逐字节相同；不同 seed => 哈希流不同 => 层级序列
不同。由 `TestReproducible` 钉住（1000 个 key，序列化字节比对）。

## 语义与代码位置

- 可复现：`levelOf` + `List.Serialize`（skip/skip.go）；`TestReproducible`
- 有序 Range：`List.Range`（skip/skip.go）；`TestRangeAgainstRef` 对照 `check.Ref`
- 增删查/Len：`Insert`/`Find`/`Delete`/`Len`（skip/skip.go）；`TestOps`
- 哨兵错误：`ErrBadRange`/`ErrDuplicate`/`ErrNotFound`（skip/skip.go）；`TestErrors`（errors.Is）
- 边界（空表 Range、删空 Len==0）：`TestOps` 首尾两段
- 比较次数上界：非导出计数器 `compares` + `Compares()`；`TestConcurrentAndBound` 断言均值 ≤64
- 并发只读：计数器用 `atomic.Int64`，结构只读；`TestConcurrentAndBound`（16 goroutine，-race）
