# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 数据模型
- 变更 `Change{Version, Op, RecID, Group, Value}`：Op ∈ Insert/Delete/Update；
  Update 携带新分组/新值，按“先在旧组删除旧记录、再在新组插入新记录”展开。
- 视图按 RecID 保存当前成员 `(group,value)`，按 group 保存各聚合器状态。
- 所有状态仅在进程内存；journal 文件写本地临时目录，自描述：
  `magic "ONTJ\1"`(5) + 记录帧；帧 = 4 字节大端长度 + 变长 JSON 体 + 4 字节 CRC32(IEEE)。

## 2. 哪些聚合能增量撤回（逐器推导）
设组 G 当前成员多重集 M，聚合值 f(M)；删除 x 后需要 f(M\\{x})。
- **Count**：f=|M|，f(M\\{x})=f(M)-1。结果只依赖基数，与 x 无关 → 可直接减，不需要成员。
- **Sum**：f=Σm，f(M\\{x})=f(M)-x。线性、可逆 → 可直接减，不需要成员。
  IEEE754 下加减不满足结合律，但“插入时加、删除同一条时减”成对抵消；
  重算按 RecID 排序累加，审计以 `math.Float64bits` 位级比较。+0/-0 视为相等（位级审计先归一化符号位）。
- **Min**：状态只保存当前最小值 m。若 x>m，新极值仍为 m，可增量；
  若 x==m，M 中是否还有另一个 m、或次小值是多少，聚合状态里没有，无法反推 →
  必须读取 M\\{x} 全成员重算。Max 对称同理。
- **DistinctCount**：状态需要“每个不同值的持有计数”才能安全撤回。
  本设计刻意只保留聚合标量（题目要求“无法从聚合状态反推”），删除某值时无法判断
  还有谁持有它 → 删除一律触发成员重算（插入可增量：首次出现的值才 +1）。

结论：`agg.Aggregator.NeedsMembersOnDelete() bool` 由聚合器显式声明；
Count/Sum 返回 false，Min/Max/DistinctCount 返回 true。

## 3. Apply → Recompute → Commit 多阶段
对每条变更，在单一互斥锁内：
1. **Apply（增量）**：先取旧成员（若有），从旧组扣减；再向新组增量加入。
   扣减/加入调用聚合器；聚合器可返回 `ErrNeedRecompute` 表示无法增量。
2. **Recompute（例外路径）**：仅当涉及组声明需要成员且确有必要（Min/Max 删到当前极值、
   DistinctCount 删除）时，用该组**当前**成员表重算该组该聚合。
   非导出计数器 `recomputeCount`、`recomputeMembersScanned` 记账；
   只扫描本组成员，扫描数 ≤ 组成员数（不变量 3）。
3. **Commit**：更新 RecID 成员表、组最大版本、空组删除（Count=0 的组整体移除，
   查询返回 `ErrGroupNotFound` 而非零值），一次性对外可见 → 读不到半更新状态。

## 4. 版本单调性与幂等
- 视图记录已应用的最大版本 V。v<V：回退，返回可判定 `ErrVersionBackward`，拒绝、计入 rejected。
- v==V：幂等丢弃，视图逐字段不变（RecID 内容相同的重复帧天然 no-op）。
- v>V：接受。缺失版本（空洞）按日志语义允许（只比较“已应用最大版本”）。
- 缺失分组键（空 Group）与 NaN 值：拒绝并计入 rejected，不产生任何状态变更。

## 5. journal 重放与截断分类
重放逐帧读取，按剩余字节数分类（均为可判定错误，重放在第一个坏帧停止）：
- 不足 5 字节头：`ErrHeaderIncomplete`（文件级头 magic）。
- 长度前缀 <4 字节：`ErrLengthIncomplete`。
- 声明长度 L，其后字节 < L：体 < L-4 为 `ErrRecordIncomplete`；
  最后 4 字节（CRC 区）残缺归 `ErrCRCMismatch`（按“CRC 无法校验通过”处理）。
- 体完整但 CRC 不符：`ErrCRCMismatch`。
重放只提交完整且校验通过的帧，半截记录绝不生效；
重放后视图 == 已生效帧的全量重算结果。

## 6. 崩溃恢复
崩溃点用注入回调模拟（不注入时无额外开销）：`crashAfterApply`、
`crashMidRecompute`、`crashBeforeCommit`，回调中 panic。
恢复 = 新建视图从头重放 journal：journal 只在 Commit 成功后落盘（WAL 先写后改内存可见状态），
故三个崩溃点恢复后都等于不崩溃的结果（原子的“整帧或不生效”）。

## 7. 并发
单把 sync.RWMutex 串行化写阶段；读用快照（RLock 下复制组结果）。
Recompute 在持锁状态下用最新成员表完成，与同组写入天然互斥，变更不会丢失。

## 8. 包划分（共 6 个包，9 个 .go 文件）
change（类型/编解码）、agg（5 个聚合器）、journal（追加/重放）、
view（状态与编排+计数器）、audit（全量重算逐组核对）、cmd/demo。
测试按包合并：change+journal 一个、agg+view 一个、audit+demo 场景一个。
