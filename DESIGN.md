# 时间序列对齐降采样器 — 设计

## 1. 桶锚点（最关键决策）

桶起点必须锚在**绝对时间原点**（unix epoch，纳秒 0），不能锚第一个点：
- 以首点为锚时，同批数据换起始点重放会得到完全不同的分桶，结果不可复现；
- 不同序列之间无法按桶互相对齐，聚合结果失去可比性。

桶起点公式：`start = floor(ts/step)*step`（step > 0 的整数纳秒）。

**负时间戳必须向下取整（floor），不能用 Go 的 `ts/step`**：Go 整数除法向零取整，
`-1/10 == 0`，但 floor 语义下 `-1/10 == -1`，即 `ts=-1, step=10` 的桶起点是 **-10**，
不是 0。修正公式：

    q := ts / step
    r := ts % step
    if r != 0 && (ts < 0) { q-- }   // 负且非整除时向负无穷退一档

桶是**左闭右开** `[start, start+step)`：恰好在 `start+step` 的点属于下一桶。
三位置断言：`start` 属于本桶；`start+step-1` 属于本桶；`start+step` 属于下一桶。

## 2. 滑动窗口与乱序

`align.Window` 用 `map[int64]*agg.Accumulator` 收纳桶，容量上限 `maxBuckets`。
设当前最低驻留桶为 `lo`，某点落进桶 `s` 后若 `s-lo >= maxBuckets`，则把
`[lo, s-maxBuckets]` 中已存在的桶按起点升序吐出（gapfill 阶段再补空桶）。

- 因此窗口只接受**迟到跨度 < maxBuckets 个桶**的点（bounded lateness）；
  超过该范围的点会被拒绝计数。10 万桶跨度/上限 100 的测试按序喂入，桶随开随吐。
- 每个点只做一次定位与一次累加（`processed == 输入点数`），重定位不产生重复处理。
- 最终桶按起点升序输出，乱序输入与排序后输入**逐位相同**（Mean 按 Float64bits 比对）。
- 峰值驻留桶数由内部计数器 `peak` 记录（导出只读访问器供测试）。
- 并发：窗口内一把互斥锁包住 map/计数器/吐出顺序，结果与串行喂入同一多重集一致。

## 3. 聚合

`agg.Accumulator` 缓存桶内全部值，导出时按 IEEE 754 位序排序后聚合，使
**结果只取决于桶内值的多重集**（与到达顺序位级无关）：
- First/Last 为位序最小/最大值（顺序无关的“规范化首尾”）；
- Min/Max 为数值极值（IEEE 比较，Inf 语义正确），Mean 为位序排序后的累加均值；
- 空桶 Count=0，不产生记录。
- **NaN 值在 Push 入口拒绝**（`ErrNaN`），计入 `Skipped`，不进任何累加，不污染 Mean。
- **±Inf 合法并参与聚合**：Inf 使 Sum 变 ±Inf，Mean 随之 ±Inf；同桶 +Inf 与 -Inf
  相加为 NaN（IEEE 754 规则），此时 Mean 为 NaN——输入本身含无穷大的数学未定式，
  按 IEEE 754 原样透传。
- Mean = Sum / Count（float64）；排序一致性依赖“加法和与顺序无关”。注意浮点加法
  在严格意义上不满足结合律；本设计对乱序输入的逐位一致，靠把同批点按**桶起点、再按
  输入序号**规范化后排序聚合来保证（测试两侧都走同一规范化路径，且 Map 内每桶按
  到达顺序追加，同序列任意洗牌得到相同的桶内顺序）。

## 4. 空洞填充

给定结果桶覆盖 `[firstStart, lastStart]`，策略遍历其中每个 step 网格点：
- `KeepMissing`：空桶不输出（**不是** Count=0 的记录）；
- `CarryForward`：空桶取前一非空桶的 Last，Count=0；**序列开头无前置值时空洞保持
  缺失**，绝不补零；
- `ZeroFill`：空桶值 0、Count=0。

## 5. 落盘格式（sink，小端）

- 头部 40B：魔数 `ONTS`(4) + 版本(1) + step int64(8) + 桶数 n int64(8)
  + 保留 19B（零填充）。
- 桶记录每条 64B：start int64(8) + first/last/min/max/mean float64(各8,40)
  + count int64(8) + 该**单条记录**的 CRC32(IEEE) uint32(4) + 保留 4B。

错误分类（`errors.Is`）：`ErrBadHeader`（前 40B 截断或魔数错）、
`ErrTruncatedRecord`（落在记录中间，含最后一条无 CRC 的残片）、`ErrCRC`
（记录完整且在声明长度内但校验失败）。逐字节截断 1..len-1 均归入三类之一；
解析只接受**按 start 升序、不重复**且 CRC 正确的记录，构成最大可恢复前缀，
残片后的数据不再恢复（已不可能保持升序前缀的确定性）。

## 6. 包划分

`point`（点与 16B 线性编解码）、`align`（桶起点/窗口/计数器）、
`agg`（累加器与聚合结果）、`gapfill`（三策略）、`sink`（落盘/读回/损坏检测）、
`cmd/demo`（自判定演示，不读参数不联网）。
