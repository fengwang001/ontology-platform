# DESIGN — 带撤回的增量聚合视图维护器

## 数据流

变更流 → 校验（拒绝计数）→ journal 追加（持久点）→ 三阶段内存应用
（Apply → Recompute → Commit）→ 视图可读。崩溃后重放 journal 重建视图。

## 一、各聚合器的撤回策略推导

设某组当前成员多重集为 M，聚合状态为标量 s。删除一条值 v 时，
问题归结为：仅由 s 与 v 能否唯一确定新聚合值？

- Count：s = |M|。删除任意一条，新值恒为 s-1，与被删的是哪条、
  值是多少无关。→ 可增量撤回，无需成员。
- Sum：s = sum(M)。被删值 v 已知，新和恒为 s-v（IEEE754 下这就是
  「重放同一操作序列」的结果，见审计节），无需知道其他成员。
  → 可增量撤回，无需成员。
- Min：s = min(M)。若 v > s，新极值仍为 s；但若 v == s，状态里
  没有次小值的信息：{1,2} 与 {1,100} 的 s 都是 1，删掉 1 后新极值
  分别是 2 与 100，仅由 s 无法区分。→ 删除命中当前极值时必须
  回到该组成员重算；未命中时无需任何操作。
- Max：与 Min 对称（v == max(M) 时触发重算）。
- DistinctCount：s 为不同值个数。删除 v 后是否减一，取决于 v 是否
  仍被其他记录持有：{a,a,b} 与 {a,b,b} 的 s 都是 2，删掉一个 a 后
  分别得 2 与 1。标量 s 不含引用计数信息。→ 每次删除都需要该组
  成员重算。

结论：agg 包中每个聚合器显式声明 IncrementalDelete() bool
（Count/Sum 为 true，Min/Max/DistinctCount 为 false），view 据此
决定是否进入 Recompute 阶段。

## 二、Recompute 触发条件（例外而非常态）

- 插入永远增量：五个聚合器都支持 Add，无重算。
- 删除/更新（更新 = 旧组删 + 新组插）时，对每个聚合器：
  - IncrementalDelete() 为真 → 直接 Sub(v)，无重算；
  - 否则询问 RemoveCausesRecompute(v)：Min 仅当 v <= 当前 min，
    Max 仅当 v >= 当前 max，DistinctCount 恒为真。
- 重算只读取该组成员，访问数 = 当时组内成员数，计入非导出计数器
  （recompute 次数 / 访问成员条数 / 单次最大访问），经 Stats()
  快照读出。删空组后整组从 Groups() 移除，查询返回「不存在」。

## 三、版本幂等与乱序判定

视图记录 lastVersion 与 lastChange。对到达的变更 c：

- c.Version > lastVersion → 接受；
- c.Version == lastVersion 且 c 与 lastChange 逐字段相等 → 幂等
  重放，视为成功但不产生任何状态变化（也不再写日志）；
- 其余（版本回退，或同版本不同内容）→ 返回可判定错误
  ErrStaleVersion 并计入 Rejected()。

判定依据：版本号由上游单调分配，视图只需与最后应用者比较即可
判定重复与回退，无需保留历史。

## 四、日志格式与截断分类

文件 = 8 字节自描述头（magic "ONTJ" + 格式版本 u16 + 保留 u16），
随后逐条记录：[len u32][payload len 字节][crc32 IEEE u32]，
CRC 覆盖 payload。payload 由 change 包编解码（版本 u64、op u8、
id u64、标志 u8（分组键是否存在）、组长 u16、组名字节、值 f64 位）。

重放按序扫描，任一截断点被确定性地分入五类之一：

- 头不完整（落在 [1,7]）→ ErrHeader
- 长度前缀不完整（落在某记录前 4 字节）→ ErrLength
- 记录体不完整（落在 payload 内）→ ErrBody
- CRC 不完整或不匹配（落在 CRC 内，或内容被改）→ ErrCRC
- 恰好落在记录边界 → 干净前缀，无错误，生效记录 = 完整前缀条数

出错时重放返回之前全部完整记录与可判定错误，半截记录不生效。

## 五、崩溃恢复与三阶段

Apply 的持久点在「journal 追加成功」。三个阶段（Apply 改成员与
增量聚合 → Recompute 例外重算 → Commit 推进 lastVersion）全部只
改内存；任一阶段崩溃后丢弃内存状态，由 Open 重放 journal 完整
记录重建，结果与不崩溃逐字段相同。校验失败/乱序的变更不落盘，
因此重放不会复活被拒绝的变更。崩溃注入点：Apply 中途、Recompute
中途、Commit 之前，经可注入的 stage hook 触发。

## 六、审计的位级相等依据

audit 的全量重算 = 用一份独立实现从头折叠同一条变更流（同样的
insert+=、delete-= 操作序列）。浮点加法不满足结合律，但同一操作
序列逐次执行的结果位级确定，因此增量视图与全量重算的 Sum 可用
math.Float64bits 逐位比对；Min/Max/Distinct 与顺序无关。

## 七、并发

视图内一把 sync.RWMutex：写（Apply）全程持写锁，读（Query/
Groups/Stats）持读锁，读者不会观察到半更新状态；Recompute 在写锁
内完成，同组并发写入不会丢失。版本号由调用方单调分配。
