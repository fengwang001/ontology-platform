# 段索引与范围回放器 设计

仅标准库；目录布局：`seg-<firstSeq>.log`（段）与 `seg-<firstSeq>.idx`（稀疏索引）。

## 1. 段文件格式（自描述）

- 头 32 字节（大端）：magic `ONSE`(4) + version uint16 + reserved uint16 +
  firstSeq uint64 + count uint64 + stride uint64。count 在 Close/repair 时原地改写。
- 头之后逐事件追加帧：`length uint32 | payload[length] | crc32 uint32`。
  CRC 为 payload 的 IEEE CRC32；length=0（空载荷）合法。
- 写以整帧为单位追加；读者可在文件末尾看到半帧，因此顺序读提供
  「严格」与「容错」两种模式，容错模式在截断/坏 CRC 处停止并返回已读前缀。

## 2. 稀疏索引：定位规则推导

锚点是 `(序号 seq, 字节偏移 offset)`：段内第 0 条记一个锚点，之后每 N 条一个。
回放 `[from,to]` 时必须选「**不大于 from 的最大锚点**」，再从该偏移顺序扫描、
跳过序号 < from 的事件，直到进入区间。

为什么不能选「最接近 from 的锚点」：最近锚点可能 **落在 from 之后**。锚点只给
正向扫描能力，不能反向取数据；从大于 from 的锚点起扫，from 与该锚点之间的事件
被永久漏掉。而不大于 from 的最大锚点保证锚点 ≤ from，跳过量 = 两锚点间的下标差，
上界为 N−1（锚点自身被跳过时差值最大 N−1；from 恰为锚点时跳过 0）。

三种位置：from 等于锚点 → 跳过 0，直接命中；from 在两锚点之间 → 跳过 1..N−1；
from 小于首锚点（锚点是段首事件时即 from < 段首）→ 从段头顺序扫，或按全局语义
夹取（见 §5）。

## 3. 索引可丢弃、可逐字节重建

索引是纯加速结构。索引里每个字段都必须能从段重新推出：
- seq：段头 firstSeq + 该锚点前的整帧计数；
- offset：从 `HeaderSize` 起顺序累加帧长；
- stride N：存在**段头**里（不放在索引专有信息中），锚点规则固定为「段内 ordinal
  为 N 的整数倍」。

因此删掉 `.idx` 后，顺序读段、按同一规则产出锚点，配合索引文件头（magic/version/
firstSeq/stride/anchorCount，全部由段推出）写回，结果与原索引**逐字节相同**。
任何「只在写入时才知道」的信息都不允许进索引。

## 4. 索引失效与回退

用锚点前先校验：seek 到 offset，必须能解出合法 length 前缀、帧完整、CRC 通过，
且该帧序号恰好等于锚点 seq。任一条件不成立（如偏移被改成事件中间）即判定索引
失效（报告标注 `StaleIndex`），自动回退为从段头全段顺序扫描，结果仍正确。

## 5. 范围语义

- `from > to`：返回可判定错误 `ErrInvalidRange`。
- `from < 全局最小序号`：不报错，夹取到最小序号（从头回放）。
- `to > 全局最大序号`：不报错，回放到实际末尾。
- 空日志返回空结果；区间恰好等于一段或跨多段均按段头 firstSeq 升序拼接。

## 6. 段边界与连续性

段按 firstSeq 升序回放；相邻段必须满足
`prev.firstSeq + prev.count == next.firstSeq`，否则报告 `ErrSequenceGap`
及缺口区间 `[prev 末尾 +1, next 首 -1]`。跨段拼接因此不重不漏。

## 7. 截断分类与 repair

对段文件逐字节截断（切点 x ∈ [1, len-1]）：
- `x < HeaderSize`：`ErrTruncatedHeader`（头部不完整）；
- 头部完整但切点落在某帧 length 字段内：`ErrTruncatedLength`；
- length 完整但 payload+crc 不足：`ErrTruncatedBody`；
- 帧边界完整但 CRC 不符：`ErrCRCMismatch`（翻转字节而非截断触发）。
四类用 `errors.Is` 区分（含 `ErrInvalidRange`、`ErrSequenceGap`）。
repair 容错扫出最大可恢复前缀，将段头 count 改写为实际条数并截断文件到帧边界，
报告改写前/后条数差。

## 8. 并发

Log 用 RWMutex：Append 持写锁（含滚动新段），Range 持读锁取段清单后独立打开文件
容错读取；整帧追加 + 容错读到帧边界为止，保证回放只见一致前缀。计数：
`SkippedEvents`（定位跳过数，须 < N）、`BytesRead`（实际读取字节）。

## 9. 复杂度上界

锚点间隔 N：定位跳过事件数 < N。读取字节 ≤ 区间内事件帧长之和 + 一个锚点区间
（≤ N 帧）的字节 + 段头，测试按此界断言。
