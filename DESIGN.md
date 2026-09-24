# 设计：带撤回的增量聚合视图维护器

## 1. 数据模型

- 变更 `Change{Op, Group, HasGroup, Ver, ID, Old, New}`：Op ∈ Insert/Delete/Update；
  Ver 单调递增；ID 为记录标识；Old/New 为记录值。
- 缺失分组键（无分组）拒绝并计入 Rejected；空串 `""` 是合法分组。
- NaN 拒绝并计入 Rejected；+0.0 与 -0.0 用 `math.Float64bits` 判定相等。
- 视图为每个组维护：成员表 `map[id]float64`（含插入顺序 id 序列），
  以及五个聚合器的实例；并记录已应用最大版本 `maxVer`。

## 2. 撤回策略推导

删除一条值 x 的记录时：

- **Count**：聚合状态仅含计数 n，删除贡献是常数 -1，n-1 即新结果，可增量。
- **Sum**：状态仅含总和 S，删除贡献是常数 -x，S-x 即新结果，可增量
  （测试值取小整数，全程在 2^53 内精确表示，增量与全量重算位级一致）。
- **Min**：状态仅含当前最小值 m。若 x>m，m 不变；若 x==m，其余记录的最小值
  无法由 m 反推（任何 ≤m 的值都可能），必须遍历该组成员重算。
- **Max**：与 Min 对称，删到当前最大值时必须遍历成员重算。
- **DistinctCount**：状态只保留「不同值集合」（按 Float64bits 归并 +0/-0）。
  删除值 v 时无法判断 v 是否还有其他记录持有，必须遍历成员值重建集合。

每个聚合器显式声明 `NeedsMembers()`；view 仅在被删值命中其敏感条件且
该组存在声明需要成员的聚合器时触发一次 Recompute。重算是例外而非常态。

## 3. Apply → Recompute → Commit

1. Apply：校验（分组/NaN/版本），把变更追加写日志（持久化点），
   更新组成员表，Count/Sum/Min/Max/DistinctCount 先做可增量更新；
   Min/Max/DistinctCount 若删到敏感值，标记该组 pending。
2. Recompute：仅对 pending 组，在事务暂存区遍历该组成员重算受影响聚合器；
   成员访问逐条计入 `recomputeTouched`，并断言不超过当前组成员数、不跨组。
3. Commit：把暂存结果写回聚合状态、推进 maxVer；删空组时整组从 map 移除
   （查询返回 ErrGroupNotFound，而非零值）。

计数器：`recomputeCount`（触发次数）、`recomputeTouched`（访问成员条数）。

## 4. 版本与幂等

Apply(v)：v < maxVer → ErrVersionRollback（拒绝计数，不污染视图）；
v == maxVer → 视为重复投递，整体丢弃，视图逐字段不变（幂等）；
v > maxVer → 正常处理，提交后 maxVer=v。依据：Ver 是变更流上的全序，
同一 Ver 唯一标识同一条变更，重复到达无新增信息。

## 5. 日志格式与截断分类

文件：魔数头 `"ONTJNL01"`（8B 自描述）+ 记录帧序列；
帧 = 4B 大端长度 n + n 字节 JSON 载荷 + 4B CRC32(IEEE) of 载荷。
重放按字节边界分类：头 <8B → ErrShortHeader；长度区 <4B → ErrShortLength；
载荷不足 n 字节 → ErrShortRecord；CRC 不符 → ErrCRC；读到完整帧的干净 EOF → 正常结束。
任何不完整/损坏帧均不生效，重放结果等于此前完整帧部分的全量重算。
打开时若末尾存在残帧，以截断方式回卷到最后一个完整帧边界（模拟崩溃恢复）。

## 6. 崩溃恢复

崩溃点钩子：Apply 中途（日志追加后、成员表更新前）、Recompute 中途、
Commit 之前。恢复 = 重开日志（回卷残帧）→ 从头重放全部完整帧到新视图。
因全部已提交变更都在日志中、未提交变更不在，恢复后视图与不崩溃运行
逐字段相同（Result 按字段、Sum 按 Float64bits 比较）。

## 7. 并发

单把 RWMutex 串行化写提交：成员表与聚合状态只在锁内变更，读取者要么看到
变更前、要么看到 Commit 后，不可能看到 Count 已变而 Sum 未变的半更新；
Recompute 持写锁在暂存区完成，同组并发写入排队串行提交，不丢失。

## 8. 包划分（≥5 个）

change（变更与编解码）、agg（聚合器族）、journal（日志追加/重放/分类）、
view（分组视图与三阶段编排、恢复）、audit（全量重算逐组核对）、cmd/demo。
