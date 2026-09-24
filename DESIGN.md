# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 数据模型

- 变更 `Change{Op, Version, ID, Group, NewGroup, Old, New}`：Op ∈ Insert/Delete/Update；
  Version 为单调递增版本号；ID 为记录主键；Group 为分组键；Old/New 为值。
  Update 表示记录 ID 从 (Group, Old) 移动到 (NewGroup, New)（同组同键也合法）。
- 分组视图按 Group 维护该组全部记录 (ID → value) 与若干聚合器状态。

## 2. 每个聚合器的撤回推导

设聚合状态仅保留“结果”，删除成员 x 时：

- **Count**：结果是基数 n，删除后必为 n−1，与 x 内容无关 → 可直接减，不需成员。
- **Sum**：结果是 Σv，删除后必为 S−x.v，减法是求和的精确逆运算 → 可直接减。
  为保证全量重算（按成员 ID 排序求和）与增量（按到达序求和）得到 **IEEE754 位级同一**
  的结果，内部不使用 float64 累加，改用 `math/big` 精确有理求和，输出时统一转 float64；
  两种路径输入同一多重集，结果位级一致。
- **Min/Max**：结果仅为当前极值 m。若删除的 x.v ≠ m，极值不变；若 x.v = m，
  状态里没有“第二小/大”的信息，无法从结果反推新极值 → 此时**例外触发**该组重算。
  因此需要成员的判定是动态的：`NeedMembersOnDelete(oldVal) = oldVal == 当前极值`。
  常态删除（不删极值）不重算，保证重算只是例外。
- **DistinctCount**：结果是 distinct 值的基数。删除 x 后必须知道“该值是否还有其他成员”，
  仅靠基数无法回答 → 每次删除都需要成员（实现里该聚合器自带 value→count 引用表，
  `NeedMembersOnDelete` 恒真；语义等价于需要该组成员信息）。

`agg.Aggregator` 接口提供 `NeedMembersOnDelete(v float64) bool`，view 在删除/更新阶段
先向该组每个聚合器询问；任一回答真即对该组触发一次 Recompute（重建该组所有聚合器）。

## 3. 多阶段维护 Apply → Recompute → Commit

1. Apply：在校验通过后先把变更追加写日志（WAL，fsync 由测试临时文件承担），在私有暂存区
   执行插入/删除的增量更新，并记录哪些组需要重算；版本号在 Apply 前判定。
2. Recompute：仅对被标记的组，按该组当前成员重放构造全新聚合器集合；计数器记录触发次数与
   访问成员条数（一次重算访问数 ≤ 该组成员数，且只访问本组 map）。
3. Commit：把暂存结果原子替换进可见状态（单 mutex 下整体发布，读者不可能看到半更新）。
   崩溃点注入（Apply 中途 / Recompute 中途 / Commit 之前）下，日志要么不含该记录，
   要么含完整记录；内存状态未提交即丢弃，重启后重放日志即得到无崩溃路径的同一状态。

## 4. 版本幂等判定依据

- 视图保存 `maxVersion`。Op.Version < maxVersion → `ErrVersionRegression`（可判定错误），拒绝。
- Op.Version == maxVersion：同一变更重放，应用后各字段不变 → 幂等成功（不重复计数、不写日志）。
- Op.Version > maxVersion：正常应用，随后 maxVersion = Op.Version。
- Update 的原子性：Delete(旧) + Insert(新) 同属一条日志记录，重放时整体生效。
- 分组键缺失（空 ID 或 Op 非法）拒绝并计入 Rejects；NaN 用 math.IsNaN 拒绝并计数；
  ±0.0 经 math.Float64bits 归一后在 DistinctCount 中视为同一值。

## 5. 日志格式与截断分类

文件 = 魔数头 `"ONTOJNL"` + u16(版本) + u32(条目数)，之后若干记录：
`u32 载荷长度 | 载荷 | uint32 CRC32(IEEE)`，载荷为 gob 编码的 Change。
重放分类：头 < 14 字节 → ErrHeaderIncomplete；长度字段不足 4 → ErrLenIncomplete；
载荷不足 → ErrBodyIncomplete；CRC 不符（含 CRC 被截断，按已落盘字节补零比对失败）
→ ErrCRCMismatch。截断点恰在记录边界则重放成功，生效数 = 完整记录数。

## 6. 恢复、并发与核对

- 恢复：新建视图，用 journal.Replay 逐条喂给 View.Apply（幂等版本保护），状态即追平。
- 并发：单 RWMutex 串行化提交；读路径在 RLock 下取不可变快照。Recompute 与同组写入在
   同一临界区内完成，写入不会丢失；-race 干净。
- audit：取当前组成员多重集做独立全量重算，逐组逐聚合器比对；float64 用
   math.Float64bits 位级比较，缺组/多组也算不一致。
