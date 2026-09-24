# 设计：只追加日志的段索引与范围回放

## 1. 磁盘格式

段文件 `seg-%016d.log`（文件名即本段首序号，可排序）：

- 段头 24 字节：`magic "SEG1"`(4) + `version uint32` + `firstSeq uint64` + `count uint64`，自描述。
- 事件记录：`length uint32`(载荷字节数) + `payload[length]` + `crc32 uint32`。
- CRC 覆盖 length 四字节 + payload；seq 不存盘，seq = 段头 firstSeq + 段内位置。
- 写入顺序：先一次 `Write` 写完整条记录，再 `WriteAt` 更新段头 count。单条记录由一次 write
  落盘，读者要么看不到、要么看到整条。

索引文件 `seg-…idx`：`magic "SPX1"`(4) + `interval uint64` + `numAnchors uint64`，
其后每个锚点为 `seq uint64 + offset uint64`（offset 指向记录 length 前缀起点，含段头 24 字节）。
锚点位置为段内事件下标 0、N、2N、…。

## 2. 稀疏定位：为什么是“不大于 from 的最大锚点”

设锚点为 a0<a1<…，from 是目标事件。索引只能给出“锚点处的字节偏移”，锚点之后的事件必须
顺序解码（变长记录）才能到达。

- 取 `max { ai.seq <= from }`：起点在 from 之前或恰好等于 from，向前顺序扫描只会跳过
  from 之前的事件，不会漏掉任何目标。
- 若取“最接近 from 的锚点”（允许在 from 之后）：该锚点之后的字节才是可读起点，锚点与
  from 之间的事件全部位于起点之前，回放无法回头读取 → 漏事件。
- 三种位置：from 恰为锚点（跳过 0）；from 在两锚点之间（跳过 1..N-1）；from 小于首锚点
  （即 from < firstSeq：无锚点，从段头后开始，from 钳到 firstSeq）。

因此跳过事件数恒为 `from - floorAnchor.seq < N`，即扫描量有上界。

## 3. 索引可丢弃、可逐字节重建

索引中每个字段都不依赖“写入时才知道”的信息：interval 是建索引参数；锚点 seq 来自
firstSeq + 下标，offset 来自从段头开始逐记录累计的编码长度。删掉索引后，顺序扫描段文件
（校验每条 CRC）即可得到完全相同的字节序列。索引里不得存放时间戳、写句柄状态等段外信息。

## 4. 索引失效与回退

使用锚点前在该 offset 处尝试解码一条记录：offset 越界、length 超上限或 CRC 不符即判锚点
无效（典型篡改：offset 指向事件中间，length 字段为垃圾或 CRC 失败）。一旦无效：

1. 放弃整个索引，从段头后全段顺序扫描定位 from；
2. 报告 `IndexInvalid = true`；
3. 结果仍保证正确（正确性只依赖段文件），代价是跳过数可能 ≥ N。

## 5. 段边界与跨段回放

段按 firstSeq 排序。合法链要求 `seg[i].firstSeq + seg[i].count == seg[i+1].firstSeq`，
否则 repair 报 `ErrSeqGap` 并给出缺口闭区间。回放对与 [from,to] 相交的段逐段读取，
段内按头 count 限定条数，跨段处由相邻段首序号衔接，天然不重不漏。

## 6. 边界语义

- 空日志（无段文件）：返回空结果，无错误。
- `from > to`：`ErrInvalidRange`（可判定）。
- from 小于最小序号：钳到最小序号，从头回放，不报错。
- to 超过最大序号：回放到末尾，不报错。
- 空载荷合法（length=0，记录仍为 8 字节）。

## 7. 并发一致性

写者先写记录后更新头 count；读者打开文件时读到某个 count=K，则前 K 条记录均已完整落盘
（更新次序由程序顺序保证）。读者只读 K 条，故每次回放看到某个一致前缀；尾部半条记录
不可见。四类损坏错误用哨兵错误区分：`ErrShortHeader`、`ErrShortLengthPrefix`、
`ErrShortBody`、`ErrCRCMismatch`（另有段间 `ErrSeqGap`、区间 `ErrInvalidRange`）。
