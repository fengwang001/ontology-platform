# 设计推导：带撤回的增量聚合视图维护器

目标：基表插入/删除/更新以变更流到达，视图按分组维护
Count/Sum/Min/Max/DistinctCount，进程内存状态 + 本地日志，可崩溃恢复。

## 1. 变更模型（change 包）

Change{Op, Group, Value, Version}：Op ∈ Insert/Delete/Update；
Update 携带 (OldGroup,OldValue,NewGroup,NewValue)，语义=删旧插新。
版本号 Version 单调递增；用 GroupOK 标志区分“空串（合法）/缺失（拒绝）”，
NaN 值在校验阶段拒绝。编码：自描述魔数头 `IVM1\n`（大端）；每条记录 =
4 字节长度前缀 + payload + 4 字节 CRC32(IEEE)。

## 2. 各聚合器撤回策略推导（agg 包）

设聚合状态 S，被删记录值 v。

- Count：S 仅为计数，删除 S←S−1，单值可确定，**可增量撤回**，无需成员。
- Sum：S=Σx，删除 S←S−v，单值可确定，**可增量撤回**，无需成员。
  位级相等前提：测试数据取整数值域（|x|<2^53 精确表示），增量与全量重算
  都按版本序累加，同序同值故 math.Float64bits 相同。
- Min：S=min。v≠S 时删后仍为 S；v=S 时状态没有“次小值”，无法反推，
  **必须扫描该组全部成员重算**（组空则删组）。Max 对称。二者 NeedsMembers=true。
- DistinctCount：需知道删除 v 后是否仍有记录持有 v；用 value→持有计数映射，
  归零则基数−1。该信息是成员派生信息，NeedsMembers=true（由频次表承载）。

触发规则（view）：删除/更新的删除半边时，只要该组某聚合器 NeedsMembers 且
被删值确实命中（Min/Max 为当前极值、DistinctCount 持有计数归零），才触发一次
Recompute；Count/Sum 永不触发。重算是例外：非导出计数 recomputeCount、
membersScanned 记录次数与访问条数。

## 3. 视图三阶段（view 包）

Apply：校验（GroupOK/非 NaN）→ 版本判定 → 暂存区执行增量更新，必要时标 dirty。
Recompute（仅 dirty 且需要时）：只遍历该组当前成员（不跨组），从空聚合重建；
访问条数 ≤ 组成员数。Commit：暂存结果一次性写回主状态（读端只见旧或新，
无半更新），推进 appliedVersion。RWMutex 保护；同组串行化使 Recompute 期间
写入不丢失。

版本幂等：记 maxVersion。Version<max → 回退错误；==max → 重复，整体跳过、
视图逐字段不变；>max 才执行。删组内最后一条时移除整组，查询返回
ErrGroupNotFound 而非零值。

## 4. 日志与恢复（journal 包）

变更先追加 WAL 再 Commit。恢复从头重放完整记录。截断点分类：头不完整 /
长度前缀不完整 / 记录体不完整 / CRC 不匹配（四类可判定错误）。半截记录不生效；
重放结果=完整前缀的全量重算。三个崩溃注入点（Apply 中途 / Recompute 中途 /
Commit 之前）崩溃后，因 Commit 前主状态不改且变更已落日志，重放结果与不崩溃一致。

## 5. 审计（audit 包）

用完整成员集合按 (group,version) 排序全量重算，逐组逐聚合器比对；
浮点用 math.Float64bits 位级比较。

## 6. 范围

仅标准库；状态进程内存；日志写 os.TempDir()；不联网、不读参数。
