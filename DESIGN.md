# 只追加日志：段索引与范围回放 — 设计推导

## 1. 磁盘格式（全部信息自描述，只用标准库）

段文件 `NNNNNN.seg`（定长头 24 字节）：

- `OSG1` magic(4) + version(4) + firstSeq uint64 LE(8) + count uint64 LE(8)。
- 每条记录：bodyLen uint32 LE(4) + body（seq uint64 LE(8) + payload）+ crc32(body) uint32 LE(4)。

追加规则：先把整条记录（长度前缀+体+CRC）写完，**再**回写头部 count。
因此任何读到 count==N 的读者都能保证第 N 条记录已完整落盘，永远不会读到半条事件；
未计入 count 的尾部字节在恢复时被视为可丢弃的残尾。

索引文件 `NNNNNN.idx`：`OID1`(4) + interval uint64(8) + anchorCount uint64(8)，
随后若干锚点 seq uint64(8) + offset uint64(8)，offset 为该事件 bodyLen 前缀的绝对字节偏移。

## 2. 定位规则：必须取「不大于 from 的最大锚点」

锚点只能把读者带到锚点事件或其**之前**，之后一律顺序前扫。

- 取「最接近 from 的锚点」可能是 seq > from 的锚点：锚点之间没有回退通道
  （只有长度前缀与向前 CRC，无法倒着解析记录边界），从那里扫描会漏掉
  `[from, anchor)` 内的事件。
- 取满足 `a.seq <= from` 的最大锚点 a：顺序扫描覆盖 `[a.seq, from]`，
  只需跳过 `from - a.seq < N` 条，必然到达 from，不漏不重。

三种位置（间隔 N）：from == 锚点序号（跳过 0 条）；两锚点之间（跳过 1..N-1 条）；
from < 首锚点（即 from < 本段 firstSeq，从段头开始，跳过 0 条）。

## 3. 索引是纯加速结构，可丢弃

锚点的两项信息（seq、offset）都能从段单独确定：

- offset：段头固定 24 字节，记录布局定长前缀，从头逐条累加即得每条记录偏移；
- seq：记录体首 8 字节就是 seq；
- interval：建索引的参数 N，重建时用同一 N（索引头里也存了一份）。

因此索引不允许携带任何「只在写入时可知」的信息；按相同 N 重建的输出是确定性的，
与原索引逐字节相同。

## 4. 索引失效与回退

定位时校验锚点：在 offset 处读长度前缀 → 读体 → CRC 校验 → seq 必须等于锚点 seq。
任一不成立（如 offset 被改成指向事件中间）即判定该索引失效：丢弃全部锚点，
从段头全段顺序扫描到 from，并在回放报告中标注 IndexInvalid。全段扫描只损失性能，
不损失正确性。

## 5. 跨段与连续性

段按 firstSeq 排序。相邻段必须满足 `prev.firstSeq + prev.count == next.firstSeq`，
否则存在序号缺口，repair 报告缺口区间 `(prevEnd, nextStart)`。
回放时取与 `[from,to]` 相交的段依次扫描，段内只读区间需要的记录，天然无缝、不重不漏。

## 6. 复杂度上界（可验证）

- 定位跳过事件数：`from - anchor.seq < N`（计数器 SkippedEvents，10 万事件 N=128，
  200 个随机 from 逐一断言）。
- 读字节数：段头 + 区间内记录编码长度之和 + 至多一个锚点区间（N-1 条记录）的定位扫描。

## 7. 边界语义

- 空日志：回放返回空结果，不报错。
- `from > to`：可判定错误 ErrInvalidRange。
- `from < 最小序号`：钳到段首从头开始（不报错）；`to > 最大序号`：回放到末尾（不报错）。
- 空载荷（payload 长度 0）合法；单事件、整段、跨三段均为正常区间。

## 8. 截断分类（逐字节注入，errors.Is 可区分）

size < 24：ErrHeaderIncomplete；记录边界处缺长度前缀：ErrLengthIncomplete；
长度前缀完整但体不足：ErrBodyIncomplete；体完整但 CRC 不足或不符：ErrCRCMismatch。
repair 保留最大可恢复前缀，把头部 count 改正为实际可读条数；残尾字节保留但不再被读取。
