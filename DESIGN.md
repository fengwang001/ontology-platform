# 带撤回的增量聚合视图维护器 — 设计

仅标准库；状态在进程内存；变更先追加到本地临时目录的日志，再于崩溃后重放恢复。

## 1. 撤回策略推导

设某组当前成员为多重集合 M，聚合状态 S=f(M)。收到一条删除 d 后，新真值为 f(M\{d})。

- **Count**：S 是基数。删除一条必使其恰好减 1，无需看其他成员 → 可增量撤回。
- **Sum**：S=Σx，线性。新值=S−d.value，浮点减法即可（位级基准） → 可增量撤回。
- **Min/Max**：S 只保存一个极值。d.value≠S 时极值不变；d.value==S 时，
  无法从 S 反推出“次小值”（M 中可能还有同值元素，也可能没有），必须遍历组成员求 f(M\{d})
  → 删除条件需要成员；删中当前极值即触发 Recompute。
- **DistinctCount**：删值 v 时必须知道 M 中是否还有别的元素等于 v（值→持有计数映射）。
  持有计数降为 0 才减基数。统一声明为“删除需要成员”，由 view 走同一条 Recompute 路径。

结论：接口 `NeedsMembers(op bool) bool` 显式声明；Count/Sum 对删除返回 false，
Min/Max（删中极值时）与 DistinctCount 返回 true。Recompute 是例外而非常态。

## 2. 多阶段与状态

每个聚合器独立维护状态：Count 为 int64；Sum 为 float64；Min/Max 存 float64+ok；
DistinctCount 存 map[float64]int（±0 归一为 +0 键；NaN 在变更入口拒绝）。

`Apply(ch)` 三阶段：
1. **Apply**：校验（版本、分组键存在、非 NaN），日志追加成功后改内存；
   Count/Sum 直接增量；对“删除需要成员”的聚合器标记受影响组。
2. **Recompute**：仅对被标记的 (组, 聚合器) 从该组现存成员全量重算该一个聚合，
   计入 recomputeCalls 与 recomputeMembersScanned；访问数 ≤ 该组当前成员数，且只遍历该组。
3. **Commit**：推进 maxVersion；先构建新组 map 再一次性替换，读者看不到半更新组。

组成员为 0 时整体删除该组；Lookup 返回 (Result, false)，区别于零值。
Update 视为同一版本内“从旧组删 + 向新组插”，同批次提交，两组都正确。

## 3. 版本与幂等

以严格递增版本号为序，判定依据：

- v > maxVersion：正常应用，提交后 maxVersion=v。
- v == maxVersion：该变更已生效，直接返回 nil，视图逐字段不变（幂等）。
- v < maxVersion：回退，返回哨兵错误 ErrVersionBackward，不产生任何写入。

乱序（到达即落后）计入 rejected；分组键缺失、NaN 同样拒绝并计数，视图不被污染。

## 4. 日志格式与截断分类

文件 = 8 字节头 `ONTWAL01`，其后为若干自提交帧：
`[u32 payloadLen][payload][u32 crc32][u32 commit=payloadLen]`，小端；
CRC 覆盖 payloadLen‖payload‖commit（含帧尾提交标记）。

逐字节截断时恰分四类：
1. 头部不完整：长度 < 8 或头魔数不符 → ErrShortHeader。
2. 长度前缀不完整：帧起点后剩余 1..3 字节 → ErrShortLength。
3. 记录体不完整：能读长度前缀，但负载或 CRC 字段本身被截断 → ErrShortRecord。
4. CRC 不匹配：CRC 字段完整但其所保护的帧尾 commit 标记被截断（rem=帧长-4..帧长-1），
   或整帧完整但内容损坏（整帧边界翻字节）→ ErrCRC。

重放返回成功解码的前缀记录数与分类错误；恢复视图 = 完整前缀记录全量重算，半截帧不生效。

## 5. 崩溃恢复

持久化点只有“日志追加并 flush 成功”。三个故障点：Apply 中途（日志已写、内存未改）、
Recompute 中途、Commit 之前。随后从同一日志重放到新 view；日志只含完整帧且版本幂等，
恢复视图与不崩溃时逐字段相同。

## 6. 并发

单一互斥锁串行化提交（Apply→Recompute→Commit 在锁内），读取快照也在锁内拷贝。
读者只看到替换后的完整组，不会出现 Count 已变而 Sum 未变。Recompute 在同一临界区完成，
同组并发写入排队，不丢失。

## 7. demo 判定项

① Min 重算恰好 3 次、Sum 0 次；② 增量 vs 全量逐组位级相等；③ 删空组查询返回不存在；
④ 截断四类各一例；⑤ 乱序拒绝计数；⑥ 三个崩溃点恢复一致；⑦ 并发提交聚合正确。
共 7 条 OK/FAIL + 总计，输出 ≤ 20 行。
