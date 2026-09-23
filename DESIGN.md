# 组提交顺序保证器 (ontology)

## 1. 目标与边界

并发写请求攒批 -> 一次落盘一次 fsync -> 每个调用方收到自己的结果与全局序号。
仅用标准库；日志写临时目录。五个包: req / batcher / wal / commit / recover。

## 2. 序号分配时机与回滚

候选方案:
- 到达即分配: 落盘失败留下空洞序号，违反“序号区间连续”。
- 落盘成功后分配: 批内条目先入盘、序号后补，崩溃恢复后无法与盘上顺序一致，
  且调用方等待期间无法获知“我排在谁后面”。

规则(本实现): 批次组装完成(关闭成批)后、写入之前，由 leader 一次性把
[seq+1, seq+n] 连续分配给批内条目(顺序=到达顺序)，随后写盘+同步。
若写或同步失败: 整批序号作废，seq 计数器回滚到分配前；批内全部调用方收到
同一错误(ErrWriteFail/ErrSyncFail)。下一批序号紧接上一成功批，无空洞。
成功提交后才唤醒等待者并返回各自结果。

## 3. 批次原子性 (批级 CRC32，非条级)

磁盘布局:

- 文件头: magic "ONTWAL01" (8B)
- 批次: [u32BE payloadLen][u64BE baseSeq][u32BE count]，每条 [u32BE len][payload]，
  最后 [u32BE CRC32-IEEE]，CRC 覆盖批头与全部条目。

CRC 在批次最后 -> 写到一半的崩溃天然缺 CRC，整批不可见。回放逐批顺序读；
任一批 CRC 不符则从该批起全部丢弃，绝不只丢坏条目。截断分类:
头部不完整(ErrHeaderShort) / 批头不完整(ErrBatchHeaderShort) /
条目不完整(ErrEntryShort) / CRC 不完整(ErrCRCMissing，正好缺尾部 4 字节) /
CRC 不匹配(ErrCRCMismatch，长度完整但内容损坏)；切在批次边界为 clean。
回放恢复到最后一个完整批次，baseSeq 连续递增。写失败时截断文件回该批起点，
保证“成功后才占用字节区间”。

## 4. leader 选择与唤醒不串台

请求进入 batcher 的 chan；负责关闭当前批的 goroutine 成为该批唯一 leader
(谁取走并凑齐/超时关闭批次谁当)，其余调用方把 *Req 留在批 entries 中并在
req.done 上阻塞。leader 落盘成功后按 entries 顺序逐个写回各自的 Seq/Err 并
close(done)；每个调用方只等自己持有的 *Req，结果不可能串台。失败同样逐条
投递同一错误后回滚序号。

## 5. 攒批触发 (条数/字节/时长，先到者)

字节计数 = len(payload)，不含长度前缀。达 maxCount 或 maxBytes 立即成批；
否则从第一个请求起等 maxWait。时钟为可注入接口 (Now/NewTimer)，测试用假时钟
推进，超时批大小=当时已收集条数。超大单条(len>maxBytes)不拒绝: 立即(连同
先到者)成批，该批可超限一次，之后恢复上限——maxBytes 约束的是“不再追加会
超限的条目”，单条本身必须能落盘。

## 6. 顺序与并发

单个提交循环串行提交批次 -> 写顺序=批序=序号序；fsync 次数=成功批数。
回放断言批序号区间 [base, base+n) 连续不重叠，批内第 i 条序号=base+i。
Close: 新请求收到 ErrClosed，排空在途请求为最后一批落盘同步后关闭文件；
竞争请求要么进最后一批成功，要么收到 ErrClosed，不存在“成功但未落盘”。

## 7. 边界语义

零请求 Close: 不产生批次，文件只含 8 字节头(有头无批)。空载荷合法(len=0)。
maxCount=1 退化为逐条提交；maxWait=0 每条立即成批。

## 8. 错误

ErrClosed / ErrHeaderShort / ErrBatchHeaderShort / ErrEntryShort /
ErrCRCMissing / ErrCRCMismatch / ErrWriteFail / ErrSyncFail，均支持 errors.Is。

## 9. FINDINGS

测试完成后填写: (1) 三触发条件的批大小与同步次数；
(2) 每类截断点字节区间、分类、回放后可见批次数与末序号。
