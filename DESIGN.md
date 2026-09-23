# 时间序列对齐降采样器设计

## 1. 桶起点锚定（核心推导）

锚点必须是**绝对时间原点（unix epoch，t=0）**，不能是第一个点的时间戳。

- 若以首点为锚，桶边界 = 首点 + k*step。同一序列换一个起始点重放，全部桶边界随之平移，结果不可复现，也无法与另一条序列按桶对齐。
- 锚定 t=0 后，桶起点只依赖绝对时间与 step：`bucketStart = floor(ts/step) * step`。任意重放、任意乱序到达，同一点永远落在同一桶。
- 时间戳是纳秒整数，必须用**数学向下取整**，不能用 Go 的 `/`：Go 整数除法向零取整。反例 `ts=-1, step=10`：数学上 `-1/10 = -0.1`，floor = -1，桶起点 = **-10**；Go 的 `-1/10 = 0`，会错误得到 0。

实现（floorDiv）：`q := ts / step; r := ts % step; if r != 0 && ((ts < 0) != (step < 0)) { q-- }`。本系统 step 必须为正，即余数非零且 ts<0 时 q--。

## 2. 桶边界：左闭右开

桶为 `[start, start+step)`。

- `ts == start`：属于本桶。
- `ts == start+step-1`：属于本桶。
- `ts == start+step`：不属于本桶，属于下一桶（其起点）。

## 3. 乱序、迟到点与确定性

点不保证按时间到达。桶内只累积点；桶完成（finalize）时先按 (ts, 到达序) 稳定排序，再聚合。
因此**任何输入排列产生完全相同的结果**：First/Last 由排序后时间序决定；Mean 用同一组值按同一顺序求和（先排序），浮点结果逐位一致，用 `math.Float64bits` 比对。

## 4. 资源与复杂度（可证明）

滑动窗口：参数 `maxBuckets` 限制同时驻留桶数。Push 到桶 b 时，若 b 与最小驻留桶差距 >= maxBuckets，则按起点升序 finalize 并吐出最旧桶。

- 任意时刻驻留桶数 <= maxBuckets（align 内非导出计数器记历史峰值）。
- 每个点仅插入其唯一桶一次，`pointsProcessed` 恰等于输入点数；迟到点只是定位到窗口内旧桶，绝不重复处理。
- 前提：迟到点不得落在已吐出水位线之前（差距 >= maxBuckets），超出报 ErrLateBeyondWindow。
- Drain() 吐出全部剩余桶。

## 5. 聚合（agg）

First（最小 ts 的值）、Last（最大 ts 的值）、Min、Max、Mean、Count。
同 ts 时按稳定排序次序，保证确定性。Mean = Sum/Count。

## 6. 空洞策略（gapfill）

输入为只有非空桶的稀疏序列，补齐区间从首个非空桶到末个非空桶：

- HoldMissing：空桶不出现（结果里没有该桶，不是 Count=0 的记录）。
- CarryForward：空桶值 = 前一个非空桶的 Last，Count=0。**开头空洞没有前值**，必须保持缺失（绝不补零）；测试显式传入开头空桶验证其被跳过。
- ZeroFill：空桶值=0、Count=0；开头空洞同样保持缺失，避免伪造时间轴范围。

## 7. NaN 与 Inf

`math.IsNaN(v)` 的点被拒绝并计入 `skipped`，不进任何桶、不污染 Mean。`±Inf` 正常参与：Min/Max/First/Last 可取 Inf；Mean 的和含 ±Inf，结果为 ±Inf（正负抵消为 NaN，属 IEEE754 语义）。

## 8. 落盘格式（sink，自描述 + CRC32）

全部小端：

- Header 24B：magic "ONTTS001"(8)、version u8、reserved 3B、step int64、maxBuckets int64。
- Record 24B/桶：start int64、count int64、value float64。
- Trailer 8B：覆盖 header+全部 record 的 CRC32(IEEE) uint32，加魔尾 "ENDB"(4)。

截断分类（errors.Is 可区分）：

- 截断长度 < 24：ErrShortHeader（恢复 0 桶）。
- (len-24)%24 != 0：ErrIncompleteRecord，恢复已完整的整记录前缀。
- 余数==0 但 trailer 缺失或 CRC 不匹配：ErrCRC，恢复全部完整记录。

恢复时按 start 排序、去重，保证升序无重复。

## 9. 错误体系

ErrInvalidStep（step<=0）、ErrNaNValue、ErrLateBeyondWindow，加 sink 三错误，均为哨兵错误。step 不整除跨度合法（最后一桶允许不满）。

## 10. 并发

Downsampler 用单一互斥锁保护桶 map 与计数器；finalize 回调在锁外执行以免重入。聚合只依赖排序后同一多重集，故并发结果与串行逐位一致。
