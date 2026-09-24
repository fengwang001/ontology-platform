# 带撤回的增量聚合视图维护器 — 设计

## 1. 聚合器撤回策略推导

记组 G 的成员多重集 M。聚合状态 S 与删除记录 r。

- **Count**：S=|M|。删除 r 后新值恒为 |M|-1，仅依赖 S，可直接减。
- **Sum**：S=Σv。删除 r 后新值为 S-v(r)，仅依赖 S 与被删值，可直接减。
- **Min/Max**：S=min(M)（Max 对称）。当 v(r)≠S 时，r 不可能是极值载体，
  新极值仍是 S，可增量保留；当 v(r)=S 时，极值是否还存在取决于其他成员，
  而 S 只是一个标量、不含“次小值/重数”信息，无法反推，必须扫描 M\{r} 重算。
- **DistinctCount**：S=|distinct(M)|。删除 v 后是否减 1，取决于 v 的重数是否为 1。
  仅持基数无法判定；本实现统一在删除时要求成员集并重算（计数器状态可做增量优化，
  但按题意声明 NeedsMembersOnDelete=true，重算作为策略基线）。

故接口让每个聚合器显式声明 `NeedsMembersOnDelete()`；view 仅在
“删除/更新且该聚合器声明需要成员”时对该组触发一次 Recompute（一个变更至多一次）。
重算是例外而非常态：Count/Sum 的重算次数恒为 0。

## 2. 变更模型与编码（change/journal）

Change{Version, Op(Insert|Delete|Update), Group, Value, RecID}；Update 携带
OldGroup/OldValue/OldID 表示被替换的旧成员。编码：

- 文件头：魔数 `"ONTOLOG"`(8B) + 版本字节(1B)，自描述。
- 每条记录：4B 大端载荷长度 + JSON 载荷 + 4B IEEE CRC32（覆盖长度与载荷）。
- 重放遇帧尾干净 EOF 正常结束；其余尾部按下列分类（从头顺序消费帧）：
  头部不完整 = 头前截断；长度前缀不完整 = 剩余 1..3 字节；
  记录体不完整 = 剩余 4..(len+3) 字节（有长度但载荷缺）；
  CRC 不匹配 = 载荷完整但 CRC 缺失/损坏（剩余 len+4..len+7 且 CRC 校验失败）。
截断只发生在最后一帧，因此至多一条半截记录不生效。

## 3. View 的多阶段维护与版本幂等

Apply(change) 分阶段：Validate → Append 日志 → Apply（生成每聚合器 add/remove
增量到暂存）→ Recompute（按需扫描成员集）→ Commit（成员集、所有聚合状态、
lastVersion 一次换入，受同一把 mutex 保护）。读者要么看到 Commit 前、要么看到
Commit 后，绝不观察到 Count 已改而 Sum 未改。崩溃注入点：Apply 中途（日志已追加、
状态未动）、Recompute 中途（暂存/成员集未动）、Commit 之前（均未动）——三处都在
提交点之前，重启重放完整日志即得到与不崩溃逐字段相同的状态。

版本判定：v < lastVersion → ErrVersionRegression（拒绝、计数、不污染、不写日志）；
v == lastVersion → 幂等丢弃，视图逐字段不变；v > lastVersion → 正常应用。
成员集删空后整组删除，查询返回 ErrGroupNotFound（不是零值）。空 Group 合法；
缺 Group、Value 为 NaN、操作不存在的 RecID 均拒绝计数。更新=旧组删+新组插。

## 4. 审计
audit 从全部已接受变更重算期望分组，与 View.Groups() 逐组逐聚合器比对；
Sum 用 math.Float64bits 位级比较。

## 5. 包划分
change（类型+JSON 编解码）、agg（5 聚合器接口与实现）、journal（追加/重放/分类）、
view（分组状态+多阶段编排+崩溃钩子）、audit（全量核对）、cmd/demo（7 条判定）。
文件总数 8（含 2 个测试文件，测试全部表驱动、单函数多 case）。
