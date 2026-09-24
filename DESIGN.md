# 设计：只追加日志的段索引与范围回放

仅标准库。一个日志目录由若干段文件 `seg-XXXX` 与索引文件 `seg-XXXX.idx` 组成。

## 1. 段文件格式（自描述）

固定段头 17 字节：`magic[4]="ONSEG"`、`ver=1`、`firstSeq uint64 BE`、`count uint32 BE`。
其后是记录流，每条记录：`length uint32 BE | payload | crc32(IEEE) uint32 BE`，
`length` 仅为 payload 长度，CRC 仅覆盖 payload。

- 事件序号不进记录：`seq(第 k 条) = firstSeq + k`。序号完全由段头与位置决定。
- 追加协议：先一次 `Write` 写完整条记录（length+payload+crc 拼成单一切片），
  成功后再原地改写头中的 `count`。因此文件中第 `count` 条之后只可能出现
  「尚未被 count 承认」的记录，且该记录要么完整要么 CRC/长度校验失败。
- 读端以文件长度 + length + CRC 三重判定半条记录，故并发追加时读到的恒为一致前缀。

## 2. 稀疏索引

索引文件：`magic[6]="ONSIDX"`、`ver=1`、`interval uint32 BE`，
其后每条锚点 16 字节：`seq uint64 BE | offset uint64 BE`，offset 为记录首字节
（length 前缀）相对段文件的绝对偏移。每写满 N 条落一个锚点，首锚点必为首条记录
（seq=firstSeq，offset=17），故锚点严格按 N 递增且可由段文件逐字节重放生成——
索引不含任何「仅写入时可知」的信息，删除后可逐字节重建。

## 3. 定位规则推导（为何必须取 floor 锚点）

回放 `[from,to]` 需找到起始记录。候选锚点必须满足 `anchor.seq <= from`，
取其中最大者（不大于 from 的最大锚点），再从该偏移顺序扫描，丢弃
`seq < from` 的记录直至 `seq == from`。

不能取「最接近 from 的锚点」：该锚点序号可能大于 from。记录流只能向后扫描，
从大于 from 的锚点出发永远遇不到 `[from, anchor.seq)` 内的事件，造成漏读，
且锚点不回存载荷，无法回退。取 floor 后，from 距该锚点最多 N-1 条，
因此定位阶段跳过事件数满足 `0 <= skipped < N`，即严格小于锚点间隔。

三种位置：
- from 恰为锚点序号：skip=0，直接命中；
- from 在两锚点之间：skip = from - anchor.seq，用上索引；
- from 小于首锚点：首锚点即段首事件，等价从头顺序扫描，skip=0。

## 4. 范围语义与跨段

- `from > to`：返回 `ErrInvalidRange`（可判定错误）。
- `from < 全局最小序号`：定义为钳到最小序号，从头回放，不报错。
- `to > 全局最大序号`：放到实际末尾，不报错。
- 多段按 `firstSeq` 排序；段内用 floor 锚点定位，读到 to 为止；后续段从段头
  顺序读。连续性不变量：`seg[i].first + seg[i].count == seg[i+1].first`。
- 字节计数器只统计记录字节（4+len(payload)+4）。最坏读取量 =
  区间内事件编码长度之和 + floor 锚点至 from 的不足一个锚点区间（< N 条）。

## 5. 损坏分类（对段文件逐截断点）

将长度 L 的段截断到 t（1 <= t < L），从头顺序解析：

- `t < 17`：`ErrShortHeader`，头部不完整；
- 在某记录起点 off 处 `t < off+4`：`ErrShortLength`，长度前缀不完整；
- `t < off+4+n`：`ErrShortBody`，事件体不完整（n 为 length 值，且需做合理性上界校验）；
- `t < off+4+n+4`：`ErrCRC`，CRC 不匹配（CRC 尾部被截断按不匹配处理）；
  完整记录但 CRC 值不符同样为 `ErrCRC`。
- t 恰在记录边界：前缀完整，无错误（正常 EOF）。

最大可恢复前缀 = 最后一条完整且 CRC 正确记录的结尾。repair 截断到该位置，
并把头中 `count` 改写为实际可读条数；改正前声明条数与实际条数之差即丢失条数。

## 6. 索引失效检测与回退

定位到锚点偏移后尝试读 length：若偏移越界、length 不合理或该记录 CRC 不符，
即判定索引与段不一致（典型：锚点被改成事件中间的偏移）。此时标记
`Report.StaleIndex=true`、错误以 `ErrStaleIndex` 可识别，并自动回退为从段头
全段扫描，仍产出正确的 `[from,to]` 结果。

## 7. 序号缺口

repair 按 firstSeq 排序所有段，逐段校验
`first + count == 下一段 first`；不成立则缺口区间
`[first+count, nextFirst-1]`，以 `ErrSeqGap` 报告。

## 8. 并发

单写者互斥；每条记录用单一切片一次 Write，随后更新头 count。读端打开独立 fd，
任何未完整写入或未被 count 承认的尾部都因 length/CRC/EOF 判定而停止，
因此并发回放只见一致前缀，事件序号必连续无缺口。

## 9. 错误与计数

哨兵错误：`ErrShortHeader/ErrShortLength/ErrShortBody/ErrCRC/ErrInvalidRange/
ErrStaleIndex/ErrSeqGap`，均可用 `errors.Is` 区分。
回放器计数器：`Skipped`（定位丢弃事件数，断言 < N）、`BytesRead`（记录字节）。
