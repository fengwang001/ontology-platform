# 设计：只追加日志的段索引与范围回放

## 1. 存储布局

每段两个文件：`NNNNNNNN.seg` 与 `NNNNNNNN.idx`。
段文件（自描述头，固定 24 字节，随后逐事件记录）：

    magic(4)="ASEG" | version(1)=1 | firstSeq(8 BE uint64) | count(8 BE uint64)
    每条记录：payloadLen(4 BE) | payload(payloadLen) | crc32(4, IEEE, 覆盖 len 前缀与 payload)

索引文件（每锚点 16 字节，自描述、无额外字段）：

    每项：seq(8 BE) | byteOffset(8 BE，相对段数据起点即头之后)

每写入 N 条事件落一个锚点（含第 0 条）。索引内容只含「序号、偏移」，
二者都能从段文件重新推出：顺序扫描段、每 N 条记录一次偏移即可，
故删除索引后重建结果与原索引逐字节相同。段头的 count 由实际可读记录数推出。

## 2. 定位规则：为什么是「不大于 from 的最大锚点」

锚点只保证「该偏移处是某条事件的起点，序号已知」。从锚点只能向后顺序扫，
不能向前。

- 若选「最接近 from」的锚点，它可能在 from **之后**：从那里开始会跳过
  from 与锚点之间的事件，造成漏读，且没有任何手段向前补读。
- 必须选 seq <= from 的最大锚点 A。锚点间隔为 N 时，from 落在
  [A.seq, A.seq+N) 内，跳过的事件数 = from - A.seq ∈ [0, N-1]，严格小于 N。

三种位置：from 等于某锚点（跳过 0 条，用上索引）；from 在两锚点之间
（跳过 1..N-1 条，用上索引）；from 小于第一个锚点——首锚点即段首事件
（seq=firstSeq），此时 from < firstSeq，按第 5 节语义从段首开始、跳过 0 条，
同样用上索引。

## 3. 回读与字节计数

段头后从锚点偏移顺序读记录；每条先读 4 字节长度，再读 payload 与 4 字节 CRC，
CRC 不符立即返回 `ErrCRCMismatch`。回放读取字节数 = 定位起点到末条记录结束，
其上界为「区间内事件编码长度之和 + 一个锚点区间（N 条记录）」。

## 4. 损坏分类与修复

对段文件按长度逐字节截断，任一截断点恰属四类之一：

1. 头部不完整（< 24 字节）：`ErrHeaderTruncated`；
2. 长度前缀不完整（剩余 < 4 字节）：`ErrLenTruncated`；
3. 事件体不完整（剩余 < payloadLen+4）：`ErrBodyTruncated`；
4. 最后一条完整记录 CRC 不符：`ErrCRCMismatch`。

修复 = 顺序扫描至首个不可读/CRC 错记录为止，保留此前最大可恢复前缀，
把段头 count 改写为实际条数，截断文件并重建索引。跨段修复另检查
`prev.firstSeq + prev.count == next.firstSeq`，违例返回缺口区间
`[prev.firstSeq+prev.count, next.firstSeq)` 与 `ErrSeqGap`。

## 5. 范围语义

`from > to` 返回 `ErrBadRange`；`from < 全局首序号` 从首事件开始（不报错，
序号本就单调、不会引入非前缀数据）；`to > 全局末序号` 回放到末尾（不报错）。
空日志回放返回空结果。载荷允许为空字节串（len=0 仍写长度前缀与 CRC）。

## 6. 索引失效回退

索引锚点偏移若指向事件中间，解长度前缀/读体/CRC 必失败（`errors.Is` 命中
第 4 节错误）。此时丢弃该索引、从段首全扫描定位，并在结果报告中置
`IndexStale=true`。四类段错误与 `ErrBadRange`、`ErrSeqGap` 均为哨兵错误。

## 7. 并发

写者持互斥锁追加并 flush；读者先 `Stat` 取当前大小（一致前缀），读该快照
字节数，故永远不会读到写了一半的记录。并发测试中断言每次回放都得到
`[firstSeq, firstSeq+k)` 连续无缺口前缀。索引按锚点追加写，同样在锁内进行。
