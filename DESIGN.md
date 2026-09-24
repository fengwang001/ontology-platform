# 设计：只追加日志的段索引与范围回放

## 1. 磁盘格式（全部内容自描述，仅标准库）

段文件 `seg-XXXXXX.log`：
- 段头：`magic(4)="OSEG"` `version(1)=1` `firstSeq(8,u64)` `count(8,u64)`，共 21 字节。
- 事件记录：`len(4,u32 小端)` `seq(8,u64)` `payload(len 字节)` `crc(4,u32)`。
  crc 覆盖 `seq || payload`，表 `crc32.IEEETable`。
- 打开既有段时先顺序校验，若尾部残缺则截断到最后一条完整记录，
  并把段头 count 改写为实际可读条数。

索引文件 `seg-XXXXXX.idx`（可丢弃，随时可由段重建）：
- `magic(4)="OSID"` `version(1)=1` `intervalN(8,u64)`，随后每条锚点
  `seq(8,u64)` `offset(8,u64)`，offset 为该事件 `len` 前缀的文件绝对偏移。
- 重建规则固定：从段首顺序扫描，每读到第 0、N、2N… 条（段内序号）记一个锚点。
  因此原索引与重建索引逐字节相同：索引不含任何“仅写入时可知”的信息。

## 2. 定位：为什么是“不大于 from 的最大锚点”

稀疏锚点只锚住每 N 条的第一条。定位 `[from,to]` 必须找到
`seq <= from` 的最大锚点，再从其 offset 顺序读、跳过 `seq < from` 的事件。

不能找“最接近 from 的锚点”：最近锚点的 seq 可能 **大于** from，
它位于目标事件之后；从它开始扫描就永久漏掉 `(上一锚点, from]` 区间。
取 `<= from` 的最大锚点保证起点不晚于目标；from 恰为锚点时跳过 0 条，
from 在两锚点之间时跳过 1..N-1 条，from 小于首锚点时从头扫（首锚点即段首）。
故定位阶段跳过事件数恒 `< N`（含等于 0），证明索引确实被使用。

锚点失效检测：从锚点 offset 读出的记录若长度前缀非法（越界/异常大）
或 seq/CRC 不符，则判定该索引不可信，回退到从段首全段顺序扫描，
并在回放报告中标记 `IndexStale=true`。

## 3. 段边界与范围回放

段头记录 firstSeq 与 count，不变量：
`下段.firstSeq == 本段.firstSeq + 本段.count`，违反即为序号缺口。
回放按 firstSeq 排序选中与 `[from,to]` 相交的连续段，逐段顺序读，
相邻段首尾序号相接，故不重不漏。

边界语义：
- `from > to`：返回 `ErrBadRange`。
- `from < 最小序号`：夹取到最小序号（从头回放），不报错。
- `to > 最大序号`：回放到末尾，不报错。
- 空日志回放：返回空结果与总条数 0。
- 空 payload 合法（len=0 的事件）。

## 4. 损坏分类（逐截断点）

设头长 H=21；第 i 条记录起点 off_i，长 R_i=4+8+L_i+4。
文件在长度 t 处截断，分类：
- `t < H`：ErrTruncHeader，头部不完整。
- 段头完整、读到记录起点 off_i：
  - `t < off_i+4`：ErrTruncLength，长度前缀不完整。
  - `off_i+4 <= t < off_i+4+8+L_i`：ErrTruncBody，事件体不完整。
  - `off_i+4+8+L_i <= t < off_i+R_i`：ErrTruncCRC，CRC 不完整/不匹配。
恢复取最大可恢复前缀（最后一条完整且 CRC 正确的记录末尾），
并把段头 count 改为实际条数。

四类错误均可 `errors.Is` 区分：ErrTruncHeader/Length/Body/CRC；
另有 ErrBadRange、ErrSeqGap、ErrIndexStale。

## 5. 并发

Writer 用互斥保护追加；每条记录单次 `Write`（或持锁内连续写）
后再对 Reader 可见。Reader 打开独立只读文件句柄，按段头声明的
“已完整前缀”读取；writer 追加与 reader 回放并发时，reader 只处理
能完整解出且 CRC 正确的记录，因此看到的永远是一致前缀，
不会读到半条事件。测试在 `-race` 下持续并发追加与回放。
