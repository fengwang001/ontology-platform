# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 数据模型

变更 `change.Change`：`Op`(Insert/Delete/Update)、`Version`(单调递增 int64)、
`OldGroup/NewGroup`、`OldValue/NewValue`。Insert 只填 New*；Delete 只填 Old*；
Update 同时填（允许把记录从 OldGroup 移到 NewGroup）。分组键缺失（既无 Old 也无
New 组）拒绝；值为 NaN 拒绝（IEEE754 中 NaN 不满足任何等值语义，无法去重/比极值）。
空串是合法键。`+0.0` 与 `-0.0` 归一为 `+0.0` 后参与比较（位级相等时归一），二者视为相等。

## 2. 聚合器撤回策略推导（核心）

聚合状态记为 A(S)，S 为某组当前成员多重集。删除一条 x 得到 S\{x}，能否仅由
A(S) 与 x 得到 A(S\{x})？

- **Count**：A(S)=|S|。删除后 = |S|-1，仅依赖计数与 x，**可直接减**。
- **Sum**：A(S)=ΣS。删除后 = ΣS - x，恒等式成立，**可直接减**。撤回是对同一次
  插入所加值做一次逆运算；比对按 `math.Float64bits` 位级进行。
- **Min**：若 x > A(S)，x 非最小，不影响结果；若 x == A(S)，A(S) 不保存次小值，
  也不知 x 是否有重复副本，**不能反推**，必须遍历成员重算。
- **Max**：与 Min 对称，删到当前最大时必须重算。
- **DistinctCount**：仅有「不同值个数」无法知道删除 x 后是否仍有其他成员持有 x，
  必须知道每个值的出现次数，等价于回到成员重算。故删除**需要成员**。

规则：`Aggregator.NeedsMembersOnDelete() bool`。Count/Sum=false；
Min/Max/DistinctCount=true。插入对全部聚合器均可增量。

## 3. View 的三阶段编排

每组维护：成员多重集(value→count)、成员总数、五个聚合当前值。
1. **Apply（增量）**：对受影响组做增量更新并同步成员多重集；标记「删除命中需成员
   聚合」的组。
2. **Recompute（例外重算）**：仅对标记组依据成员多重集全量重算。非导出计数器
   `recomputeCount` 与 `recomputeMembers` 精确记账；访问数 ≤ 该组当前成员数，只访问本组。
3. **Commit**：同一把锁内发布结果。注入点（Apply 中途 / Recompute 中途 / Commit
   之前）均在发布前，未完成批次对外不可见。

成员删空的组整体删除；查询返回 `(result, ok=false)`，不留零值空组。

## 4. 版本幂等与乱序

视图记录高水位 `applied`。v < applied → 可判定错误 `ErrStaleVersion`（拒绝并计数）；
v == applied → 幂等跳过，视图逐字段不变；v > applied 接受。乱序非递增一律拒绝、
不动状态。版本随变更写日志，恢复后重获高水位。

## 5. Journal 格式与恢复

布局：头 `ONTJ1\n`(6B)；其后每条 `[4B 大端长度 n][nB JSON][4B CRC32(JSON)]`。
重放分类哨兵错误：头不足 ErrShortHeader；长度 4B 不足 ErrShortLength；
记录体不足 ErrShortBody；CRC 不符 ErrCRC。半截记录丢弃，恢复视图=完整前缀全量重算结果。

## 6. 并发

单把 `sync.RWMutex`：写路径全程持写锁（Apply→Recompute→Commit 原子），读路径持读锁
取整组快照，不可能读到半更新；Recompute 与同组写入同临界区串行，不丢失。

## 7. 崩溃点恢复

三个注入点均在写锁内、发布前失败，状态停留在提交前快照；随后以同一日志重放，
恢复视图必须与无崩溃完整运行逐字段（含 Float64bits）相同。
