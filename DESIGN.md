# 带撤回的增量聚合视图维护器 — 设计

## 1. 模型
变更 `Change{Ver, Op, ID, Group, Value}`：Op∈{Insert,Delete,Update}，Update 携带旧组 `OldGroup/OldValue`。
记录 ID 唯一，组键可为空串；缺失（空 ID）拒绝。Value 为 NaN 拒绝；+0/-0 视为同值。
日志帧（自描述）：16B 头(magic "ON"、版本、payload 长度、CRC32 IEEE) + payload，
payload = 1B flags(低2位: 1 insert/2 delete/3 update；位3 oldgroup 存在) + 8B ver + 2B idlen + id
+ 2B glen + group + 8B value；update 再追加 2B oldglen + oldgroup + 8B oldvalue。
截断分类：偏移 < 12 头不完整；12..15 长度前缀不完整（长度字段位于头末 4B）；
体偏移 < 16+len 体不完整；体完整但 CRC 不符为 CRC 不匹配；恰好帧边界为正常结束。

## 2. 聚合器撤回推导
增量删除 = 仅凭 (旧聚合态, 被删值) 推出新态。
- Count：态是计数，删一条 n→n-1，可减，无需成员。
- Sum：态是标量和，新和 = 和 − v（加法可逆），可减，无需成员。
- Min：态只存极值 m。被删值 v≠m 时极值不变；v=m 时新极值是其余成员的 min，
  聚合态不含其余成员，信息已丢失 → 必须取该组成员重算。Max 对称。
- DistinctCount：态存不同值集合。删 v 后，只有成员表能判定 v 是否仍被其他记录持有，
  故也需要成员重算。
故接口声明 `NeedsMembersOnDelete() bool`：Count/Sum=false；Min/Max/DistinctCount=true。
`MinNeedsMembers` 仅在“非空组且 v==当前极值”时为 true；空组删除只删记录不重算。

## 3. 视图编排（Apply→Recompute→Commit）
每组成员表 `map[id]value` + 五个聚合器。处理一变更：
Apply：先 WAL append+Sync，再改成员表与 Count/Sum（update 视为旧删+新插）；
标记受影响组及该组是否有聚合器请求成员（Count/Sum 永不触发）。
Recompute：仅对被标记组，从该组自己的成员表全量重算请求成员的聚合器；
访问计数只累加该组成员，绝不跨组；组空则整体消失（查询返回不存在）。
Commit：推进 maxVersion（崩溃点在推进前/后均可由 WAL 重放恢复）。

## 4. 版本与幂等
日志先于状态变更落盘。重放时：v<maxVer → ErrVersionRollback（可判定）；
v==maxVer 视为已应用，直接跳过，视图逐字段不变（幂等）；v>maxVer 才应用。
重放截断日志：有效帧全部重放（允许乱序帧存在，按规则拒绝），半截帧不生效。

## 5. 恢复与并发
WAL 中每条变更先 Sync 后改内存，故 Apply 中/Recompute 中/Commit 前崩溃，
重放都能得到与未崩溃完全相同的最终态（提交即等价于“日志已含该变更”）。
RWMutex 保护整组多字段更新：写者持写锁完成一整个变更后才释放，
读者持读锁取快照，因此不可能读到 Count 已变 Sum 未变的半更新；
Recompute 与后续写入在同一把锁内串行，同组写入不丢失。

## 6. 核对
audit 独立保存“已接受变更序列”，全量重算参考态，逐组比对五个聚合；
Sum 用 `math.Float64bits` 位级比较。测试值取 2^53 以内整数，保证加减精确、位级稳定。

## 7. 包划分（≥5）
change 编解码；agg 五聚合器；journal WAL/重放/截断分类；view 编排与恢复；
audit 全量核对；cmd/demo 演示。文件均 <200 行，.go 总数 ≤24，测试表驱动。
