# 带撤回的增量聚合视图维护器 — 设计

## 1. 数据模型
- `Change{Op, Rec, OldGroup, Ver}`，Op ∈ Insert/Delete/Update；Rec 含 ID、Group、Value、Ver。
- 编码（change 包）：自描述记录体，字段定序，标志位区分「分组为空串（合法）」与「分组缺失（拒绝）」。

## 2. 聚合器撤回推导
聚合状态 f(S)，删除成员 x 后需得到 f(S\{x})。
- Count: f(S)=|S|，f(S\{x})=f(S)-1，与 x 无关，可直接减。
- Sum: f(S)=Σv，f(S\{x})=f(S)-x，可直接减。
- Min: f(S)=min S。若 x≠当前极值，结果不变；若 x=当前极值，
  无法从单一标量推出次小值，必须遍历组成员重算。
- Max: 与 Min 对称，删到当前最大值时必须重算。
- DistinctCount: f(S)=|distinct(S)|，删除 x 后是否减 1 取决于
  是否还有其他成员持有 x，标量状态无法回答，必须遍历成员。
结论：Min/Max/DistinctCount 声明「删除需要成员」；view 在删除
（含 Update 的移出半边）命中该类聚合器时触发 Recompute 阶段。
Recompute 只访问该组当前成员，是例外而非常态。

## 3. 三阶段编排 Apply → Recompute → Commit
- Apply：校验变更（分组缺失、NaN 拒绝并计数），版本判定，作用于
  影子聚合与成员表；删除命中需要成员的聚合器时置脏标记。
- Recompute：仅对脏组，遍历该组全部当前成员重建聚合器。
- Commit：单 mutex 下将影子结果发布到可见状态，并记录最大版本。
读操作持同一锁取快照，因此永远观察不到「Count 已更新而 Sum 未更新」。

## 4. 版本与幂等
ver < maxVer 返回 ErrStaleVersion（可判定，计入拒绝数）；
ver == maxVer 且变更字节相同（同版本同内容重放/重传）视为幂等，成功且不改状态；
同版本不同内容返回 ErrDuplicateVersion。日志重放天然依赖该幂等性。

## 5. 日志（journal）
文件 = 9 字节头（magic "ONTWAL" + 版本字节 + flags）
     + 若干帧 [4 字节大端长度][记录体][CRC32(IEEE) of 记录体]。
重放截断分类：头部不足→ErrShortHeader；长度前缀不足→ErrShortLength；
声明长度超出文件→ErrShortRecord；体完整但 CRC 不符→ErrCRC。
半截帧不得生效：只有 CRC 校验通过的帧才会被应用。

## 6. 崩溃恢复
顺序：先 Append 日志（持久化）→ Apply → Recompute → Commit。
注入点 Apply 中途 / Recompute 中途 / Commit 之前，均发生在日志落盘之后；
恢复时重放已确认帧即可得到与不崩溃逐字段相同的视图。

## 7. 边界与正确性
- 组删空：成员表为空时整体删除该组，查询返回 (零值,false)，Groups() 不含该组。
- NaN（math.IsNaN）拒绝并计数；+0/-0 归一化为 +0，视为相等。
- Update = 从 OldGroup 删 + 加入 NewGroup，两组都正确维护。
- audit 用基表全部成员全量重算逐组比对；Sum 以 math.Float64bits 位级比较。
- 并发：单 mutex 串行化提交、快照式读取；重算在锁内对同组成员进行，
  同组并发写入不会丢失。
