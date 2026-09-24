# 快照读可见性判定设计

## 1. 为什么只看提交时刻不够

版本由事务 T 写入。朴素规则：`T.commit < snapshot.watermark` 即判可见。

反例（测试直接构造该逻辑状态）：快照创建时水位 `watermark=10`，T1 尚未提交，
故 `T1 ∈ snapshot.active`；之后 T1 提交，`commit=5 < 10`。按朴素规则判可见，
但读者取快照那一刻 T1 尚未提交——读者看到了快照点不存在的版本。
活跃集记录的是「快照点仍在进行中的事务」，提交号与水位的数值关系并非充分条件。

正确规则：
- T 已提交 **且** `T.commit < watermark`（左闭右开，等于水位不可见）
  **且** `T ∉ snapshot.active` → 可见；
- 例外：`T == snapshot.self`（自己写的自己可见）；
- T 已回滚，或不满足上述任一条 → 不可见。

## 2. 结构与复杂度

- `txn.Table`：`map[TxnID]entry{state, commit}`，查一次状态记 1 次查找。
- `snapshot.Snapshot`：`watermark` + `active map[TxnID]struct{}`（self 也在其中）
  + `released atomic.Bool`；占用项数恒等于活跃集大小，与事务总数无关。
- 判定 = 活跃集命中 1 次 + 事务表 1 次，共 ≤2 次常数查找，不遍历事务表。
- 查找计数是每次判定的局部值并随结果返回，不挂在共享快照上，并发互不串台。

## 3. 可判定错误

`ErrUnknownTxn`、`ErrSnapshotReleased`、`ErrCorruptChain`（版本链 commit 非递增）、
`ErrActiveLimit`（活跃集超上限），均可用 `errors.Is` 区分。

## 4. 朴素参考

仅测试使用：遍历事务表与活跃列表逐项比对提交状态，与正式实现随机对拍一万组。
