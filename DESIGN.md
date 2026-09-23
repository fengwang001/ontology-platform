# 段索引与范围回放器 设计

模块 `ontology`，仅标准库。包：`event`（事件编解码）、`segment`（段追加写/顺序读）、
`sparse`（稀疏索引）、`replay`（范围回放+日志门面）、`repair`（检测与修复）、`cmd/demo`。

## 文件格式

段文件（小端）：

- 头（24B）：magic `"OSEG"`(4) + version u32(4) + firstSeq u64(8) + count u64(8)。
- 每条事件：len u32(4) + payload(len) + crc32 IEEE(4)，crc 覆盖 `len||payload`。
- 段内序号隐式：`seq = firstSeq + 事件在段内的下标`，故记录里不存序号。

索引文件（纯加速、可丢弃）：magic `"OSIX"` + version u32 + 间隔 N u32 + 锚点数 u32，
随后每锚点 `(seq u64, offset u64)`，offset 指向该事件长度前缀的字节偏移。
每 N 条记一个锚点，第 0 条（段首）必为锚点。

## 定位推导：为什么是「不大于 from 的最大锚点」

锚点把段切成长度为 N 的区间。回放 `[from,to]` 的正确起点必须满足：
起点对应序号 `a <= from`，且从 `a` 顺序扫描必经过 `from`。

- 若取「最接近 from 的锚点」：当 from 落在区间后半时，最近锚点是下一个区间的
  左端点 `a' > from`。从 `a'` 开始扫，序号 `from..a'-1` 的事件被跳过——漏数据。
  最近锚点只保证距离近，不保证 `a' <= from`，故不可用。
- 取「不大于 from 的最大锚点」`a = max{ s : s <= from }`：锚点即区间左端点，
  `from - a = (from - firstSeq) mod N < N`，扫描量有严格上界 N，且不重不漏。
- `from < 首锚点`（首锚点即 firstSeq）：没有可用锚点，从段头（offset=24）扫起；
  由于段内序号均 `>= firstSeq > from`，从首事件起即满足条件，跳过数为 0。

定位阶段跳过事件数 `= from - a < N`，测试用 10 万事件、N=128、200 个随机
from 断言严格小于 N。读取字节只计事件区扫描字节：上界为
「区间编码长度和 + 一个锚点区间」。

## 索引可丢弃、可逐字节重建

索引内容只能是段内可推导的信息：锚点序号 = firstSeq + i*N（来自段头与记录
计数），锚点偏移 = 顺序扫描段文件时各记录的起始位置（来自段内长度前缀），
N 是建索引参数（也存入索引头）。任何「仅写入时可知」的信息（如内存状态、
时间戳）都不得入索引。重建 = 扫段文件 + 同一编码函数，故与原索引逐字节相同。

## 索引失效与回退

定位时先验证锚点：在 anchor.offset 处必须能解出合法记录（长度前缀不越界、
CRC 匹配）。失败即判定索引失效：报告置 `IndexInvalid`，自动回退为从段头
全段扫描，结果仍然正确。`repair.ValidateIndex` 可单独校验索引与段一致性。

## 损坏分类（均可 `errors.Is` 区分）

- `segment.ErrHeaderIncomplete`：文件 < 24B。
- `event.ErrLengthIncomplete`：某事件剩余 < 4B，长度前缀读不出。
- `event.ErrBodyIncomplete`：长度已知但 payload 不足 len。
- `event.ErrCRCMismatch`：CRC 字段缺失（截断）或校验不符（篡改）。
- 截断恰好落在记录边界：合法短前缀，头部 count 与实际不符，由 repair 改正。

`repair.RepairSegment` 把文件截到最大可读前缀并把段头 count 改为实际条数；
`repair.CheckContinuity` 检出相邻段序号缺口（`ErrSeqGap`，携带缺口区间）。

## 边界语义（文档定义，测试锁定）

- 空日志：返回空，不报错。单事件：正常。空载荷：合法。
- `from > to`：`replay.ErrInvalidRange`，可判定错误。
- `from < 最小序号`：从头回放（钳到最小序号），不报错。
- `to > 最大序号`：回放到末尾，不报错。`from > 最大序号`：返回空。
- 跨段：段头 firstSeq 连续（`首序号+事件数==下段首序号`），范围可跨多段，不重不漏。

## 并发

`replay.Log` 用互斥锁保护追加；每条记录单次 `Write` 落盘后在锁内推进计数。
回放快照段清单后用自己的文件句柄读，只解码「完整记录前缀」，写了一半的
尾部记录视为日志末尾。因此并发下每次回放看到的都是连续无缺口的一致前缀，
`-race` 干净。
