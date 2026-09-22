# AUDIT — 不变量与复杂度核对

## 不变量逐条核对

1. **子树最大右端点始终正确**
   - 保证位置：`node/node.go` `Update()` 在每次结构变化后重算；
     `tree/tree.go` 的 `insert`/`remove`/`rebalance`/两个旋转在返回前
     一律调用 `Update`；`tree.Check()` 递归核验每个节点 `MaxHi` 等于实际值。
   - 钉住测试：`tree.TestRandomModel`（5000 步随机增删后每步调 `Check`）、
     `tree.TestMultisetDeleteOne`、`query.TestVsNaive`。
2. **查询不漏不多**
   - 保证位置：`query/query.go` 的 `overlap`/`stab` 与朴素扫描共用
     `ival.Overlaps`/`Interval.Contains` 谓词；剪枝只跳过
     `MaxHi <= q.Lo` 或 `Lo >= q.Hi` 的必然不命中子树。
   - 钉住测试：`query.TestVsNaive`（固定种子 60 轮随机对照线性扫描）。
3. **结果顺序确定且与插入顺序无关**
   - 保证位置：`query/query.go` `sorted()` 按 `ival.Compare`
     全序 (Lo, Hi, Payload) 排序后返回。
   - 钉住测试：`query.TestOrderIndependent`（8 种插入顺序逐位相同）、
     `query.TestConcurrentQueries`。
4. **删除彻底，多重集删除一次只删一个**
   - 保证位置：`tree/tree.go` `remove` 命中等键节点即摘除一个并停止；
     找不到返回 `ErrNotFound`。
   - 钉住测试：`tree.TestMultisetDeleteOne`、`query.TestMultisetDeleteViaQuery`、
     `tree.TestRandomModel`（与多重集模型逐步对账）。
5. **空与零长度**
   - 保证位置：空树查询在 `Root()==nil` 时直接返回空集；
     零长度区间合法可插入（`ival.Valid` 允许 `Lo==Hi`），
     但 `Contains`/`Overlaps` 谓词使其对任何查询不可见。
   - 钉住测试：`ival.TestOverlaps`/`ival.TestContains`（零长度表项）、
     `tree.TestZeroLengthInsertDelete`、`query.TestSemanticsAndErrors`。

## 复杂度实测（`query` 非导出计数器 `visited`）

- `query.TestStabVisitedBound`：N=1000 时点查访问 **10** 个节点，
  N=100000 时 **17** 个（上限断言 100，且 100 倍数据增长不超过 2 倍访问）。
- `query.TestOverlapVisitedScalesWithMatches`：N=100000 中命中 M=1 访问
  **17** 个节点，M=50 访问 **67** 个（上限断言 4M+200，且远小于 N/100）。
- 计数器为非导出字段 `query.Querier.visited`（`atomic.Int64`），
  不出现在任何公开接口；并发查询下它是唯一共享可写状态。
