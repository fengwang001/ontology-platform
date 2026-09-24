# 组提交与顺序保证器设计

模块 `ontology`，仅标准库。WAL 日志写本地临时目录（`os.MkdirTemp`）。

## 1. 序号分配时机与回滚

- 到达即分配：落盘失败后序号出现空洞；落盘后才分配：调用方拿不到先后信息。
- 规则：**批次组装完成、落盘之前**保留序号区间 `[seq+1, seq+n]`（firstSeq 在
  此刻确定并写入批头）；`AppendBatch` 与 `Sync` 都成功后，计数器才前进
  `seq += n`；任一失败，保留作废，计数器不动（即回滚），整批调用方收到错误。
- 计数器只由组提交循环这一个 goroutine 修改，无需锁；失败后下一批 firstSeq
  紧接上一个成功批，无空洞。

## 2. 批次原子性（批级 CRC32，非条级）

文件格式：

- 文件头（8B）：magic `OWL1` 4B + version uint16 + flags uint16，自描述。
- 批次记录：`totalLen uint32 | firstSeq uint64 | count uint32 |
  (len uint32 | payload)* | crc32 uint32`；totalLen 含自身与 CRC；
  CRC（IEEE）覆盖该记录除 CRC 外的全部字节。

读回语义：

- 数据不足文件头 → `ErrHeaderIncomplete`；不足 16B 批头 → `ErrBatchHeaderIncomplete`。
- 有批头但可用字节不足 `totalLen-4`（条目区被截）→ `ErrEntryIncomplete`，
  截断性错误一律停在最后一个完整批次。
- 条目区完整但 CRC 字段被截（缺 1..4B）或 CRC 不符 → `ErrCRCMismatch`：
  完整性不可证明，**整批丢弃，绝不只丢坏的一条**；之后若还有字节，按
  totalLen 跳过该批继续扫描，故"前后批次正常"。
- 四类错误均为哨兵错误，可用 `errors.Is` 区分；另加 `req.ErrClosed`。
- 崩溃写了一半 → 停在最后一个完整批次，下次 firstSeq = 末批 firstSeq+n。

## 3. leader 与唤醒不串台

组提交循环是固定的唯一 leader：所有调用方把 `Request{Payload, chan Result}`
投入提交通道后阻塞在自己的结果 channel（cap=1）。leader 攒批、保留序号、
写盘、同步，然后**按批内顺序**把各自的 `Result{Seq, Err}` 发回各自 channel。
等待者只监听自己的结果 channel，结果只发给本人，故不可能串台；同一批内
条目顺序即序号顺序。批内只有一次 write+fsync，其余协程不碰磁盘。

## 4. 攒批触发（batcher）

三条件先到先触发：`MaxCount` 条数、`MaxBytes` 累计载荷字节、`MaxWait`
等待时长（首条进入时启动定时器）。定时器以
`NewTimer(d) (c <-chan time.Time, stop func())` 注入，测试用手动通道。
- `MaxWait==0`：每条立即成批。
- `MaxCount==1`：退化为逐条提交。
- 单条载荷 > MaxBytes：**不拒绝**，空批 + 该条立即单独成批（字节可超限，
  仅此特例）；普通批历史最大字节不越界，commit 用非导出 maxBatchBytes 记录。

## 5. 并发关闭

`Submit` 在互斥锁下检查 closed 标志并向提交通道发送（发送期间持锁；leader
从不取该锁，不会死锁）。`Close` 取锁、置 closed、再通知 leader。因此任何
通过 closed 检查的请求，必然已被 leader 收到并并入最后一批落盘；其余收到
`req.ErrClosed`。不存在"返回成功但没落盘"。

## 6. 边界语义

- 零请求 Close：文件在 Open 时即创建并写入 8B 文件头；零批不写，落盘为
  **只有头的文件**，回放 0 批、nextSeq=起始序号。
- 空载荷合法：len=0 的条目正常读写。
- 超大单请求：单独成批，字节上限仅此特例可突破。

## 7. 包划分

`req` 请求/结果/关闭错误；`batcher` 攒批状态机（可注入时钟）；`wal`
自描述日志写/读/编码与四类错误；`commit` 组提交编排（sync 计数、
maxBatchBytes 均为非导出计数器字段，导出只读访问器）；`recover` 回放，
返回完整批列表与 nextSeq；`cmd/demo` 验收演示。
