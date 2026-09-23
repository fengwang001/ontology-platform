# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 撤回能力推导（聚合状态是否足以反推删除结果）
记组内成员为多重集 M，删除记录 x。
- Count(M)=|M|：Count(M-x)=Count(M)-1，状态只需计数，删除可直接减。
- Sum(M)=Σv：Sum(M-x)=Sum(M)-x，被删值已知，直接减。
- Min(M)：仅保存当前最小值。若 x>Min 极值不变；若 x=Min，新极值是 M-x
  中“第二小”的元素，而它不在聚合状态里，无法反推，必须访问全组成员重算。
  Max 同理（x=Max 时丢失“第二大”）。
- DistinctCount=|{distinct v}|：仅保存去重个数。删除 x 后若还有其他成员
  持有 x 则个数不变，否则减一；“是否还有持有者”无法从单一计数反推，必须
  访问该组成员，按重算处理。
每个聚合器显式声明 NeedsMembersOnDelete；view 仅在其声明需要且删除确实
发生于该组时进入 Recompute。Count/Sum 永不重算；Min/Max 仅在“删到当前
极值”时重算（删除前判断 x 是否等于极值）；DistinctCount 每次删除重算。
重算是例外面非常态。

## 2. 视图编排 Apply -> Recompute -> Commit（同一把写锁内）
- Apply：校验并更新派生状态。插入写成员表并对各聚合 Insert；删除先做可
  直接减的聚合（Count/Sum），同时判断 Min/Max 是否命中极值，命中则标记
  dirty；更新=删除旧记录+插入新记录（跨组时两组分别处理）。
- Recompute：对 dirty 聚合器只遍历“该组当前成员”逐成员重建；访问数有
  上界 |M|，成员表 map[group]map[recID]value 按组隔离，绝不扫描别组。
- Commit：整体替换发布新快照。移除组内最后一条成员时删除整组，不留
  Count=0 空组；查询不存在的组返回 (零值,false)。

## 3. 版本幂等与乱序
每条变更带全局单调版本号 v，视图记录 lastVersion。
- v<lastVersion：回退，返回 ErrVersionRollback，拒绝计数，视图不变。
- v==lastVersion：同一变更重投，幂等丢弃，视图逐字段不变。
- v>lastVersion：接受；允许跳号（版本是流位置而非连续序号）。
依据：以 lastVersion 为唯一应用边界、以版本号去重。缺失分组（Group==nil）
与 NaN 在入口校验拒绝并计数，不污染状态。

## 4. 日志格式与截断分类
文件：magic "IVWJ\x01"(5)+版本字节(1)=6 字节自描述头。
记录：4 字节大端长度 n + n 字节负载 + 4 字节 IEEE CRC32(负载)。
负载：op(1) version(u64) idLen(u16) id groupFlag(1) [groupLen(u16) group]
value(f64)；更新追加 newGroupFlag/[newGroup]/newValue。
按截断后长度 L 分类：L<6 头部不完整 ErrShortHeader；L>=6 而剩余<4
长度前缀不完整 ErrShortLength；声明 n 但实际体不足 记录体不完整
ErrShortBody；体齐而尾 CRC 不足 4 字节或 CRC 不符 ErrCRC。重放遇错即停
并返回已完整应用条数，半截记录不生效，故重放视图==完整前缀的全量重算。

## 5. 恢复模型
变更先追加日志（Sync 后）再 Apply。注入点：Apply 中途 / Recompute 中途 /
Commit 之前；崩溃均在“日志已落盘、视图未发布完成”。重放重建，凭版本
幂等与原子发布，恢复结果与无崩溃路径逐字段相同。

## 6. 并发
单把 RWMutex：写路径三阶段在同一写临界区完成；读者持读锁取整组快照，
不可能读到 Count 已更而 Sum 未更。Recompute 与后续写串行，同组写入不
丢失。-race 验证。

## 7. 边界
空视图、单组单记录、全记录同组均合法；空串分组合法（用 *string 区分
“缺失”）；成员相等按 v==v' 判定，+0.0==-0.0 为真故正负零视为相等；
NaN 拒绝；更新跨组时旧组走删除新组走插入，两组聚合各自正确。
