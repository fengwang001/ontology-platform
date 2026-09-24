# AUDIT — 不变量与复杂度核对

## 不变量

1. **跨度精确**：任一节点任一层的跨度 = 该指针跨过的底层元素个数。
   保证位置：`list/list.go` `Insert`（新节点跨度 = 前驱旧跨度 − 位次差）、
   `Delete`（被删节点跨度并入前驱再减一）；`SelfCheck` 用底层位次表逐层核对。
   钉住测试：`TestSpanInvariantUnderOps`、`TestMultisetSemantics`（删后 SelfCheck）。
2. **结构与插入顺序无关**：层高是键的纯函数（`key/key.go` `Level`），
   重复键按插入先后相邻排列且同层；结构等价判定在 `list/list.go` `Equal`
   （逐层比较键与跨度序列）。钉住测试：`TestOrderIndependence`（12 种循环移位排列）。
3. **排名自洽**：`At`/`RankOf`/`Range` 全部经由 `list.Bound`/`list.At` 的
   同一跨度游走实现（`rank/rank.go`）；`Range(lo,hi)=ub(hi)−lb(lo)`。
   钉住测试：`TestRankConsistency`（互逆 + 区间计数 = RankOf 差值，表驱动）。
4. **多重集语义**：重复键各自成节点、底层相邻；`RankOf` 取下界即第一个副本
   （`list.Bound(k,false)`），`Delete` 只删 `update[0]` 之后一个节点，
   `Count` = 上界 − 下界（`mset/mset.go`）。钉住测试：`TestMultisetSemantics`。
5. **迭代器不撒谎**：快照语义，`iter.New` 在创建时物化键序列，
   之后写入不影响迭代（`iter/iter.go`）。钉住测试：`TestIteratorSnapshot`
   （迭代中删除当前元素仍发出原序列）。

## 故障注入

- `At` 越界 → `list.ErrOutOfRange`；`Range` lo>hi → `rank.ErrBadRange`：
  `TestRankErrors`、`TestLimitsAndErrors`。
- 总数/层数超限 → `list.ErrTooManyElements` / `list.ErrLevelLimit`，
  检查先于任何结构修改（`list.Insert` 开头）；删除不存在 → `list.ErrNotFound`；
  拒绝后仍可正常读写：`TestLimitsAndErrors`。

## 并发

只读路径（`At`/`RankOf`/`Range`/`Count`/`Iterate`）不写共享结构；
唯一共享写是 `rank.Querier.visited`（`atomic.Int64`，非导出字段）。
钉住测试：`TestConcurrentReads`（16 goroutine 结果逐位相同），
`go test -race ./...` 干净。

## 访问节点数实测（上限 80）

| 元素数 | At 访问 | RankOf 访问 |
|---|---|---|
| 1000 | 6 | 5 |
| 100000 | 14 | 13 |

元素数增长 100 倍，访问节点数仅约翻倍（O(log n)），
远未线性增长。钉住测试：`TestVisitedNodesSublinear`。
