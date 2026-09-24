# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 数据模型

变更 `Change{Op, ID, Group, NewGroup, Value, Ver}`：Op 为 Insert/Delete/Update；ID
标识一条基表记录；Group 为分组键（空串合法，空串缺失指字段为空且来源未提供，
统一按 `Group==""` 判定——空串本身合法，故“缺失”定义为编码中缺失该字段）；
Value 为 float64，NaN 拒绝并计数，+0/-0 经规范化位 `Float64bits(0)` 视为相等。
版本号 Ver 单调递增；View 记录已应用的最大版本。

## 2. 哪些聚合可以撤回（核心推导）

撤回 = 从聚合状态中扣除一条被删记录的贡献。关键在于“聚合状态是否可逆”。

- Count：状态即计数 n，删除贡献恒为 -1，n-1 永远正确。可增量撤回。
- Sum：状态即代数和 S，删除贡献是 -v，结合律/逆元存在，S-v 永远正确
  （浮点为确定性减法，重算与增量都按同一存储序列相加，故位级一致）。可增量撤回。
- Min：状态只保留当前极值 m。若删除值 v≠m，极值不变；若 v==m（极值不唯一
  或唯一），仅凭 m 无法知道次小值，必须扫描该组全部成员取 min。需要成员重算。
- Max：与 Min 对称。需要成员重算。
- DistinctCount：状态若只存不同值个数，删除某值时无法判断“该值是否仍被其他
  成员持有”。即使持有频次表也会增加状态复杂度；按题意统一声明需要成员，删除
  时由成员集合重算 distinct 集合。需要成员重算。

结论：每个聚合器实现 `NeedsMembersOnDelete() bool`；Count/Sum 为 false，
Min/Max/DistinctCount 为 true。Insert/Update 的新值贡献对所有聚合都可增量
（Min/Max 直接比较，DistinctCount 查集合）。

## 3. Recompute 触发条件（例外而非常态）

删除（含 Update 的“移出旧组”）一条成员后，仅当该组中存在
`NeedsMembersOnDelete()==true` 的聚合器，且被删值需要复核时触发一次该组
重算：Min/Max 仅当“被删值的规范化位 == 当前极值位”才触发；DistinctCount
只要删除发生即触发（保守正确）。重算只扫描该组当前成员，访问条数 ≤ 组成员数，
绝不跨组。非导出计数：`recomputes`（触发次数）、`membersRead`（扫描条数）。
组内删空时删除整个分组键：`Groups()` 不含该组，`Get` 返回 (零值,false)。

## 4. 三阶段 Apply → Recompute → Commit 与崩溃恢复

Apply：校验（操作合法、键非缺失、Value 非 NaN）→ 版本闸门 → 变更成员存储与
各聚合增量；Recompute：按需扫描该组成员重建受影响聚合；Commit：写 WAL 追加
日志，落盘成功后提升 `appliedVer`。注入点 hook(apply/recompute/commit) 在对应
阶段中段 panic 模拟崩溃：因 Commit 未完成，日志中不含该条，重放 WAL 后视图
恰为“日志中完整记录”的状态，三注入点恢复结果逐字段相同。

## 5. Journal 格式与截断分类

文件 = 头(魔数 `ONTOJNL1`+版本字节，共 9 字节) + 若干帧。
帧 = 大端 4 字节长度 n + n 字节载荷 + 大端 4 字节 CRC32(IEEE，覆盖载荷)。
截断点分类（对含 200 条记录的文件逐字节截断 1..len-1）：
- 长度 < 9：头部不完整；
- 某帧剩余 < 4：长度前缀不完整；
- 剩余 ≥ 4 但 < 4+n：记录体不完整；
- 剩余 ≥ 4+n 但 < 8+n（CRC 区被切）或 CRC 校验失败：CRC 不匹配。
重放遇任一错误即停，已完整帧全部生效，半截帧不生效。

## 6. 版本幂等判定

Ver <= appliedVer 一律拒绝（Ver== 为重复、幂等丢弃；Ver< 为回退，返回
ErrVersionBackward 可判定错误），计入 Rejected，视图与成员存储不变。重复
应用同一完整日志（崩溃后重放）因此天然幂等。

## 7. 并发与核对

View 以单一互斥锁保护成员表、各组聚合与 appliedVer；读接口在锁内拷贝快照，
故读端不可能看到“Count 已变 Sum 未变”的半更新。Recompute 与写入在同锁内
串行，同组并发写入不丢失。-race 干净。audit 包独立保存全部记录做全量重算，
按组比较；float 用 `math.Float64bits` 位级比较。
