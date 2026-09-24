Design: append-only segmented log with sparse index

1. 磁盘布局
  段文件 seg-NNNN:
    header(32B) = "ONTOSEG1"(8) | firstSeq uint64 BE | count uint32 BE | reserved(12 zero)
    frame      = len uint32 BE | seq uint64 BE | payload | CRC32 uint32 BE
    CRC32(IEEE) 覆盖 len|seq|payload（含长度前缀）。
  索引文件 seg-NNNN.idx:
    "ONTOIDX1"(8) | 重复 entry{seq uint64, off uint64}；off 为帧首相对帧区起点
    （即绝对偏移 -32），使锚点定位与段头长度解耦。

2. 稀疏定位规则
  回放区间 [from,to] 时，必须选「序号 <= from 的最大锚点」，再从该偏移顺序扫描，
  丢弃 seq<from 的帧，直到 seq>to。
  不能选「最接近 from 的锚点」：最接近可能是大于 from 的锚点（from 位于两锚点
  之间偏后，或锚点粒度较大时）。从更大锚点开始会永久漏掉
  (前一锚点, 该锚点] 之间的事件，它们不出现在后续任何读取范围内。
  锚点间隔 N 时，from 与锚点距离最多 N-1，故定位跳过事件数严格 < N。
  from 小于首锚点（含无锚点）时从头（header 后）扫描，跳过 0 条。

3. 索引可丢弃、可逐字节重建
  索引只是 (seq, offset) 缓存：seq 在帧头、offset 由「header 长度 + 各帧长度」
  顺序累加得到。帧长 = 4+8+len(payload)+4，全部可从段字节推出。
  故索引不得保存只在写入时可知的信息；重建 = 顺序读全部帧、每 N 条记一锚点，
  与原索引逐字节相同。

4. 段边界与范围语义
  段头 firstSeq+count == 下一段 firstSeq（序号连续）。跨段按段序拼接，
  覆盖整段或跨三段均不重不漏。
  from<最小序号 => 从头开始（不报错）；to>最大序号 => 放到末尾（不报错）；
  from>to => ErrBadRange；空日志上的合法区间 => 空结果。

5. 损坏分类（截断点 b，header 长 H=32）
  0<b<H            头部不完整 ErrShortHeader
  H<=b 且停在长度前缀      长度前缀不完整 ErrShortLength
  读到完整长度、停在帧体内  事件体不完整 ErrShortFrame
  帧边界完整但校验失败     CRC 不匹配 ErrCRC（字节翻转类非截断损坏）
  repair 截断文件到最后一个完整帧边界，并把 header.count 改成实际可读条数。

6. 索引失效回退
  锚点偏移指向帧中间时，该处读不出合法长度前缀（越界/剩余不足）或 CRC 不符。
  任一锚点校验失败即作废本轮定位，标记 stale，回退段首全段扫描，结果仍正确，
  报告 Stale=true。

7. 并发
  写路径在 Log 上加互斥，追加完整帧后更新 header.count；读路径持读锁，
  只在完整帧边界可见事件，回放始终看到一致前缀。

8. 字节预算
  读取器逐帧精确读取（len/seq/payload/crc 各自 ReadFull），Read 计数即接触字节。
  区间回放读字节 <= 区间事件编码总长 + 一个锚点间隔内帧长（最坏 N 帧，
  因跳过严格少于 N 条）。

包划分: event / segment / sparse / replay / repair / cmd/demo。
