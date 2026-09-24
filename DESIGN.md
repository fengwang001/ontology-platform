# DESIGN — append-only segment index & range replay

只追加事件日志：顺序切成段文件，每段配稀疏索引；索引是纯派生数据，可随时删除重建。

## 1. 磁盘布局

- 事件 `Event{Seq uint64, Payload []byte}`，载荷可为空字节串。
- 段文件 `seg-NNNN`：固定 32 字节头（魔数 `ONLOGSEG1` + base 首序号 u64 + count 事件数 u64，大端，余 7 字节零填充），其后逐事件记录帧：`[len u32][payload][crc u32]`，CRC32-IEEE 校验 payload。
- 段按容量滚动；count 在关闭时（或 repair 时）写回。段内序号连续。
- 索引文件 `seg-NNNN.idx`：固定 16 字节头（魔数 `ONLOGIDX1` + 锚点间隔 N u64，余 7 字节零）+ 每条 16 字节锚点 `[seq u64][offset u64]`，offset 为段文件内帧起始字节。

## 2. 稀疏索引定位推导

要回放 `[from, to]`，回放必须从一条序号 `<= from` 的已知帧起点开始顺序读，直到真的命中 from。
锚点只覆盖序号为 `base + kN` 的事件，且锚点偏移只在“帧起点”这一类位置合法。

- 选「最接近 from 的锚点」可能取到序号 **大于** from 的锚点：从那里只能向后扫，
  from 与该锚点之间的事件永远丢失。
- 因此规则是：取 **不大于 from 的最大锚点**（upper-bound 二分后回退一格）。
  若该锚点序号为 s，只需顺序跳过 `from-s` 条；由于相邻锚点间隔 N，必有
  `0 <= from-s < N`，故「定位跳过事件数严格小于 N」。
- from 小于第一个锚点（含段首）时无锚点可用，从段头之后、即 base 处全段前缀扫描；
  跳过数 = from-base，同样 `< N`（首个锚点就是 base+N 之前唯一情形）。

## 3. 索引可丢弃、可逐字节重建

索引内只有 `(seq, offset)` 与间隔 N，而 seq 由段头 base 与该帧在段内的次序唯一确定，
offset 是扫描段文件时由“上一帧起点 + 帧长度”累加得到，N 由配置决定。
不允许把任何仅写入时可知的信息（创建时间、随机盐、句柄状态等）放进索引；
因此从头顺序解码段文件、每 N 条记一个锚点，重建结果与原索引 **逐字节相同**。

## 4. 范围回放与跨段

- 段按 base 升序；相邻段必须满足 `base_i + count_i == base_{i+1}`，repair 校验并报缺口。
- 回放对每段用 §2 定位，顺序读帧：seq < from 丢弃（计入跳过数），`from <= seq <= to` 输出，seq > to 停。
- 跨段时上一段末序号 +1 即下一段 base，天然不重不漏。

## 5. 索引失效与回退

定位时校验锚点：偏移处必须能解出合法长度前缀且帧 CRC 通过、且该帧序号等于锚点序号。
任一不成立（如偏移被改成指向事件中部）即判索引损坏：报告 `IndexStale=true`，
丢弃索引，从段首顺序扫描定位——段文件本身是唯一事实来源，结果仍正确。

## 6. 损坏检测与 repair

段顺序读遇截断，按剩余字节分类（四类均可 `errors.Is` 区分）：

| 情形 | 判定 |
|---|---|
| 不足 32 字节头 | ErrHeaderTruncated |
| 帧起点不足 4 字节 | ErrLengthTruncated |
| 声明 len/CRC 未取全 | ErrBodyTruncated |
| 帧完整但 CRC 不符 | ErrCRCMismatch |

恰在完整帧边界截断时，已读帧数少于头 count：记为下一帧的 ErrLengthTruncated（0 字节也属长度前缀不完整）。
repair 找到最大可恢复前缀（连续通过 CRC 的帧数 k），把 count 改写成 k，截断尾部；
随后顺序读不再报错。索引一律按修复后段重建。段间序号缺口另报 ErrSeqGap（含缺口区间）。

## 7. 并发

日志用 `sync.RWMutex`：Append 持写锁，整帧单次 `Write`（len+payload+crc 一次性提交）；
回放持读锁。读者只能看到「某个一致前缀」：要么看不到在写帧，要么看到完整帧。

## 8. 边界语义

- 空日志：合法；单事件：合法；空 payload：合法。
- `from > to`：返回 ErrBadRange（可判定错误）。
- `from < 最小序号`：夹紧为最小序号，从头回放，不报错。
- `to > 最大序号`：回放到末尾，不报错。
