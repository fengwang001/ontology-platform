# DESIGN — 只追加日志的段索引与范围回放器

## 文件格式

- 事件编码（`event`）：`seq uint64 BE ‖ payload`，定长 8 字节头 + 原始载荷，空载荷合法。
- 段文件（`segment`）：自描述头 28 字节 = `magic "OSEG"(4) ‖ indexEvery uint64(8) ‖ firstSeq uint64(8) ‖ count uint64(8)`；
  随后逐条记录：`len uint32 BE ‖ 事件编码 ‖ crc32 IEEE(4)`，CRC 覆盖事件编码字节。
  `count` 每追加一条即回写头部，头部是「本段首序号 + 事件数」的唯一权威来源。
- 索引文件（`sparse`）：`magic "OIDX"(4) ‖ every uint64(8) ‖ n uint64(8) ‖ n × (seq uint64 ‖ offset int64)`。
  锚点取 `(seq-firstSeq) % every == 0` 的事件，offset 为该事件记录在段文件中的字节偏移。

## 定位规则推导：为什么是「不大于 from 的最大锚点」

锚点集合 A = {a0<a1<…} 按序号严格递增，覆盖的扫描区间是 [aᵢ, aᵢ₊₁)。
要回放 [from, to]，必须从一个「序号 ≤ from 且位置已知合法」的点开始顺序扫描，否则无从对齐记录边界。

- 若选「不大于 from 的最大锚点」a = max{aᵢ | aᵢ ≤ from}：从 a 扫到 from 跳过的事件数 = from − a < every，上界严格小于锚点间隔 N。正确且有界。
- 若选「最接近 from 的锚点」：当 from 落在区间后半段时，最近锚点是 aᵢ₊₁ > from。从 aᵢ₊₁ 开始扫，[from, aᵢ₊₁) 之间的事件永远读不到 —— 漏数据；若强行回退则等价于重新找前驱锚点。因此「最近」规则在 from 位于两锚点之间偏后时必然出错，只有「不大于 from 的最大锚点」对所有位置（等于锚点 / 两锚点之间 / 小于首锚点）一致正确。
- from 小于首锚点（即小于段首序号）时无锚点可用，回退到段头之后的第一条记录（offset = 28）从头扫，跳过数 = from − firstSeq，同样 < N（首锚点在 firstSeq 处）。

## 索引可丢弃、可从段逐字节重建的理由

索引是纯加速结构，其全部内容必须能由段文件推导：

- `every`：写入段头 `indexEvery`，段自描述，不依赖索引记忆；
- 每个锚点的 `seq`：段内事件自带序号，第 k 条事件序号 = firstSeq + k；
- 每个锚点的 `offset`：顺序扫描段文件、按长度前缀累加即得；
- 锚点选取规则 `(seq-firstSeq)%every==0`：确定性算法。

因此「删索引 → 扫描段 → 重建」产出的字节流与原索引逐字节相同。反之，任何只在写入时可知、段中没有的信息（如内存计数、时间戳）都不得进索引，否则重建失败。测试断言重建前后索引文件字节相等。

## 索引失效时的回退策略

定位后先验证锚点：在 anchor.offset 处读记录，要求长度前缀可解、记录完整、CRC 通过、且解码出的 seq == anchor.seq。任一不满足即判定索引失效（如偏移被篡改指向事件中间），此时：

1. 在报告中置 `IndexInvalid = true`；
2. 回退到该段事件区起点（offset = 28）全段顺序扫描，结果仍然正确；
3. 修复侧由 `repair` 从段重建索引覆盖坏文件。

## 段边界与跨段回放

段 k 的头记录 (firstSeqₖ, countₖ)。相邻段必须满足 firstSeqₖ + countₖ == firstSeqₖ₊₁（序号连续），`repair.CheckGaps` 据此检出缺口区间 [firstSeqₖ+countₖ, firstSeqₖ₊₁−1]。
回放 [from, to] 时按段头区间裁剪：每段处理 [max(from,firstSeqₖ), min(to, lastSeqₖ)]，段间拼接不重不漏。

## 边界语义（定义并测试）

- 空日志：回放返回空结果，不报错。
- from > to：可判定错误 `replay.ErrRange`。
- from 小于最小序号：**从头开始回放**（clamp 到 minSeq），不报错 —— 语义为「回放与 [from,to] 相交的已有前缀」。
- to 超过最大序号：回放到末尾，不报错。
- 区间恰等于一整段、跨三段、空载荷：均合法。

## 故障分类（errors.Is 可区分）

- `segment.ErrHeaderIncomplete`：文件 < 28 字节头。
- `segment.ErrLengthPrefixIncomplete`：记录起点可读但长度前缀不足 4 字节。
- `segment.ErrEventBodyIncomplete`：长度前缀可读但事件体或 CRC 字节不足。
- `segment.ErrCRCMismatch`：字节完整但 CRC 校验失败（位损坏）。
- `repair.ErrSeqGap`：相邻段序号不连续。

截断点分类：t < 28 → 头部不完整；t 落在某记录的长度前缀内 → 长度前缀不完整；落在事件体/CRC 内 → 事件体不完整。截断不会产生 CRC 不匹配（字节只是缺失而非错误），CRC 类由位翻转注入覆盖。

## 并发一致性

`replay.Log` 用 `sync.RWMutex`：Append 持写锁（写记录 + 回写头 count 为一次临界区），Replay 持读锁。回放因此只能看到「某个一致前缀」，绝不会读到半条事件；`-race` 干净。

## 复杂度上界

- 定位跳过事件数 < N（N = 锚点间隔），由「不大于 from 的最大锚点」性质保证，计数器 `Skipped` 断言。
- 读取字节数 ≤ 区间事件编码总长 + 一个锚点区间的记录字节（含长度前缀与 CRC 开销），计数器 `BytesRead` 断言。
