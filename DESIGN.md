# 组提交与顺序保证器 — 设计推导

## 1. 目标与边界

并发写请求攒批、一次落盘一次 `fsync`，每个调用方拿到自己那条的结果与全局序号。
六个包：`req`（请求/结果）、`batcher`（攒批）、`wal`（落盘/读回）、
`commit`（组提交编排）、`recover`（回放）、`cmd/demo`。

## 2. 序号分配时机（核心推导）

- 到达即分配：落盘失败会留下永久空洞，违背“全局序号连续”。
- 成功后分配：调用方无从知道“我排在谁后面”，且无法在写文件前确定序号。
- **规则：批次组装完成后、落盘之前，由唯一 coordinator 连续分配整批序号；
  批次落盘/同步失败时整批序号作废，计数器回滚到本批起始值，全批调用方收到错误。**
  coordinator 单协程串行分配，故“分配—可能回滚—下一批分配”天然原子，无锁竞态。
  第 2 批失败后，第 3 批序号紧接第 1 批，无空洞。

## 3. 批次原子性：记录格式与批级 CRC

磁盘上每个批次是一条自描述记录（大端）：

`magic(4)=0x4F4E544C | seq(8) 本批首序号 | n(4) 条数 | payloadLen(4) |
entries[payloadLen]: 每条 len(4)+data | crc32(4)`

头固定 20 字节，末尾 4 字节 CRC 覆盖“头 + entries”。
CRC 是**批级**而非条级：条内任何一字节被改都会使整批 CRC 不符 → 整批丢弃，
绝不只丢坏条目。文件是记录流；零请求 Close 时**创建仅含文件头 magic(4)**
的文件，代表一个已存在但为空的日志（有测试）。

截断四类判定（按记录起始偏移 b 定位）：

- 文件 < 文件头(4)：ErrHeaderIncomplete
- b+20 > size：ErrBatchHeaderIncomplete（批头不完整）
- b+20+payloadLen+4 > size：ErrEntryIncomplete（条目区/CRC 未写全）
- 长度够但 CRC 不符：ErrCRCMismatch

## 4. 攒批器（batcher）

三个触发条件先到先得：`maxEntries` 条、`maxBytes` 字节（按 len(data)+4 计）、
`maxWait` 等待时长（注入 `Clock`：Now + NewTimer，测试用假时钟）。
**单条超 `maxBytes` 不拒绝**：若当前批非空先冲掉，超大条单独成批（字节可超限），
即“批上限约束攒批结果，不约束合法请求”。`maxWait==0` ⇒ 每条到达立即成批；
空载荷合法。

## 5. leader 选择与唤醒不串台

`commit.Committer` 内一个 coordinator goroutine 是唯一可能落盘的 leader；
请求经无缓冲 channel 递交，结果通过**每个请求自带的独立 response chan**
（容量 1，在 `req` 中分配）回送，按请求指针逐个送达，不共享槽位，
故等待者只可能收到自己那条，绝不串台。

coordinator 循环：收集一个触发批 → 分配序号区间 → 调 `wal.Append`（写+fsync）
→ 成功逐个 `respond(req, Result{Seq})`；失败回滚 seq 计数后逐个
`respond(req, Result{Err})`。同步次数 = 成功 Append 次数，记录于非导出
`syncs` 计数器。Close：置关闭态并排空在途请求，纳入最后一批落盘；
更晚到达者收 `ErrClosed`。保证“成功必落盘”：成功结果只在 Append 返回后发送。

## 6. 故障注入与回放

`wal.FileLog` 写本地临时目录；`Sync`、`Write` 均可注入失败（包装 `failableFile`）。
`recover.Replay` 顺序扫描：解析失败即停，返回此前所有**完整**批次；
中途崩溃的半批自然不可见，提交器从“最后完整批次的末序号 +1”继续。
四类截断错误由 `recover.Classify` 返回，可用 `errors.Is` 区分
（ErrHeaderIncomplete/ErrBatchHeaderIncomplete/ErrEntryIncomplete/ErrCRCMismatch）。

## 7. 复杂度与顺序不变量

N 请求、上限 B：同步次数 ≤ ceil(N/B)+少量（字节/超时批），与 N 无关于每请求。
单协程落盘 ⇒ 日志中批次序号区间连续、不重叠；批内条目顺序即序号顺序，
由“组装顺序 = 分配顺序 = 写盘顺序”保证。
