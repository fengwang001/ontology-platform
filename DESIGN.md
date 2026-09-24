# 带撤回的增量聚合视图维护器 — 设计

## 1. 数据模型

- 变更 `Change{Op, RecID, Group, HasGroup, Value, Version}`：Op ∈ Insert/Delete/Update，Version 单调递增。
- 记录 `Record{ID, Group, Value}`。视图按 Group 维护成员集合（RecID → Record）与聚合状态。
- `HasGroup=false` 表示分组键缺失（拒绝并计数）；`HasGroup=true, Group=""` 为空串键（合法）。
- NaN 不允许作为值：NaN != NaN，破坏成员相等与 Sum 位级比对，Apply 前拒绝并计数。
- 键比较：+0 与 -0 的 Float64bits 不同，但语义为同一数值（用 `v == other` 判定，该式对二者成立）。

## 2. 哪些聚合能增量撤回（推导）

设聚合状态 S、被删记录的值 v、组内其余成员集合 R。

- **Count**：S 仅为标量计数，删除恒为 S' = S - 1，与 v 无关 ⇒ 可增量，无需成员。
- **Sum**：求和可逆，S' = S - v（浮点减法确定）；+0/-0 相减结果一致 ⇒ 可增量，无需成员。
- **Min**：增量维护只保存当前极值。若 v ≠ S，新极值仍是 S；若 v == S（删的正是极值），
  新极值为 min(R)，而 R 的任何信息都不在状态里，无法从 S 反推 ⇒ 必须回组重算。
- **Max**：与 Min 对称，删除唯一极值时无法反推次大值 ⇒ 必须回组重算。
- **DistinctCount**：删除 v 后要回答“R 中是否还有 v”。仅保存 distinct 集合时无法区分持有次数；
  需保存值→持有的引用计数才能增量。为满足“删除需要成员”的语义，统一声明需要成员并触发重算，
  由组内成员集合重建 distinct 集合。

结论：每个聚合器显式声明 `RequiresMembersOnDelete bool`。Insert 全部可增量；
Delete 仅 Count/Sum 可增量，Min/Max/DistinctCount 需要成员。

## 3. 多阶段维护 Apply → Recompute → Commit

1. Apply：版本闸门（去重/回退判定）→ 校验（缺组、NaN）→ 修改成员集合 → 对声明无需成员的聚合器直接增量。
2. Recompute：仅当删除（或更新移出）影响到声明 `RequiresMembersOnDelete` 的聚合器时触发；
   用变更后的组成员整体重算这些聚合器。重算是例外：访问成员数 ≤ 当前组成员数，绝不扫描别组。
3. Commit：把暂存结果发布到视图，推进 `AppliedVersion`。三阶段在同一互斥锁内完成，
   读者只看到“旧视图或新视图”，不可能读到 Count 已变而 Sum 未变的半更新状态。

非导出计数器：每聚合器 `recomputeCount`、`recomputeMembersScanned`；另有拒绝数。
组成员删空时删除整个组，`Lookup` 返回 (零值, false) 而非零值聚合。

## 4. 版本幂等与回退

视图记录已应用的最大版本 V：
- Change.Version < V ⇒ `ErrVersionRollback`（可判定），拒绝，不污染视图。
- Change.Version == V ⇒ 视为重复投递，幂等丢弃并返回 nil（不重复执行任何修改）。
- Change.Version > V ⇒ 正常应用。
依据：版本号是变更在流中的唯一全序位置；同版本即同一条变更，重放结果必须与“只执行一次”逐字段相同。

## 5. Journal（崩溃恢复）

文件 = 头(`IVMA1` + 8 字节 big-endian 版本游标，初始 0) + 若干记录帧。
帧 = 4 字节大端长度 n + n 字节 JSON + 4 字节 CRC32(IEEE, 覆盖 JSON)。
重放按字节消费并分类截断：
- 头剩余不足 12 字节 → ErrHeaderTruncated；
- 长度字段不足 4 字节 → ErrLengthTruncated；
- 声明长度超过剩余文件长度 → ErrRecordTruncated（体或其 CRC 不完整）；
- 体完整但 CRC 不符 → ErrCRC；
末尾恰好在帧边界结束属正常 EOF，不是截断。
恢复协议：提交新变更前先追加整帧（fsync 由 Write 完成），再更新视图；
崩溃点注入于 Apply 中 / Recompute 中 / Commit 前。恢复 = 用日志完整帧重放到新视图，
崩溃只可能发生在“帧已完整落盘但视图未推进”之间，重放幂等 ⇒ 与不崩溃逐字段相同。

## 6. 并发

View 内单一 sync.RWMutex 串行化提交；快照在 RLock 下拷贝组键与聚合值。
Recompute 与后续写入同锁串行，成员集合以同一把锁保护，不丢更新。

## 7. 包划分

change（记录与编解码）、agg（五聚合器族）、journal（追加/重放）、view（编排）、audit（全量核对）、cmd/demo。
