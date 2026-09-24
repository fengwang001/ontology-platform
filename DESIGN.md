# 只追加日志：段索引与范围回放 设计

## 1. 磁盘布局

目录内文件：`000001.log`/`000001.idx`、`000002.log`/……。日志只追加。

段头（32 字节，定长，便于截断分类）：

- magic(4)="ONTL"；version(1)=1；flags(1)；firstSeq(8, 大端)；count(8)

事件记录（`event` 编解码）：`len(4 大端) + seq(8) + payload + crc32-IEEE(4)`。
len 为「seq+payload」长度，CRC 覆盖同段字节。空 payload 合法。

段尾追加时 count 滞后，段满（条数达容量）`Seal` 时回写 count。

索引文件（定长记录，可逐字节重建）：
magic(8)="ONTIDX01" + stride(uint32) + 若干锚点；
锚点 = `seq(uint64) + offset(uint64)`，每 stride 条事件记一个，
首锚点为段内第 1 条（offset=32），之后第 1+stride 条……
索引只存「序号、字节偏移、stride」，三者都能从段推出：
顺序读段时按记录边界累计字节偏移、按 stride 取模即可，故删索引可完全重建。

## 2. 定位规则推导

回放 [from,to] 必须从 **不大于 from 的最大锚点** 起顺序扫描。

- 锚点是稀疏采样，两锚点之间没有任何记录位置信息。
- 若选「数值上最接近 from」的锚点，它可能 > from（from 落在
  前半区间时最近点在右侧）。记录只追加、只能顺序向前解析，
  从右侧锚点无法回退，[锚点.seq, from) 与目标起点一起被跳过 → 漏事件。
- 选 ≤from 的最大锚点：起点位置确定合法，向前扫描只会落在
  from 之前，跳过条数 < stride，到达 from 前不会越过目标。

三种位置（stride=N）：

- from == 某锚点序号：跳过 0 条，用上索引；
- from 在两锚点之间：跳过 1..N-1 条，用上索引，严格 < N；
- from < 首锚点：回退段头（offset=32），扫描 0..N-1 条。

「小于最小序号」语义：从头开始由范围裁剪，不报错；
to 超过末尾同理回放到段尾，不报错。from>to 报 ErrBadRange。

## 3. 跨段与连续序号

段头记 `firstSeq` 与 `count`；段 k 末序号 = first+count-1，
必须满足 `first_{k+1} == first_k + count_k`，否则 repair 报
ErrSequenceGap 并给出缺口 [want, got)。

回放按序号升序逐段进行：定位起始段（首段 firstSeq≤from
且下一段 firstSeq>from），段内用稀疏锚点定位，逐段顺序读到
to 所在段，天然不重不漏。区间可恰好等于整段或跨三段。

## 4. 读字节预算

Replayer 内部计数 `skippedEvents`（定位阶段丢弃条数）与
`bytesRead`（仅 .log 读取字节，不含索引）。预算：
`bytesRead ≤ Σ(recordLen, seq∈[anchorSeq, to])`，即区间内
事件编码长度之和 + 至多一个锚点区间（起始锚点到 from 之前）。
段尾无锚点时起点为段头（offset=32），跳过仍 < stride。

## 5. 损坏分类与修复

读段尾残片时按剩余字节分类（四哨兵错误，errors.Is 可判）：

- ErrHeaderTruncated：字节偏移 < 32（头部不完整）；
- ErrLenTruncated：残片 < 4 字节（取不出长度前缀）；
- ErrBodyTruncated：4 ≤ 残片 < 4+len（事件体不完整）；
- ErrCRCMismatch：残片 ≥ 4+len 但 crc 不符（含 crc 域被截断）。

记录恰好完整到边界不属损坏（是合法前缀）。repair 顺序读
「最大可恢复前缀」，在第一条残片处截断文件，回写 count=
实际完整条数，再顺序读验证；索引整体删除重建，保证一致。

## 6. 索引失效与回退

索引锚点偏移可能被篡改（指向记录中间）。定位后从该偏移
读记录：长度前缀越界/异常大、或 CRC 不符，即判索引失效
（ErrIndexInvalid），自动丢弃索引、从段头全段扫描定位，
结果仍正确；回放报告带 `IndexInvalid` 标注。

## 7. 并发

log 层用 RWMutex：Append 持写锁，Replay 持读锁快照段列表后
以独立只读句柄扫描。每条记录 CRC+长度前缀校验通过才交付：
半条记录在回放看来就是段尾残片，跳过不交付。故并发回放恒见
连续无缺口的一致前缀。

## 8. 边界语义

空日志合法（无段）；单事件、空 payload、区间=整段、
跨三段正常；段满按容量滚动新段，序号全局连续。
