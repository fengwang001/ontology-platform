# 时间序列对齐降采样器 — 设计推导

## 1. 桶起点锚点

桶必须锚在**绝对时间原点 ts=0**，桶起点 `b = floor(ts/step)*step`。

- 不能用首个点的时间戳作锚：换一个起点重放同一批数据，分桶整体平移，
  结果不可复现，也无法与其它序列按桶对齐；首点还可能是迟到点。
- 必须用**向负无穷取整**。Go 整数除法是向零取整：`-1/10 == 0`，
  会把 `ts=-1, step=10` 错误归到起点 0 的桶。正确结果是起点 **-10**。
  实现：`q := ts / step; if ts%step != 0 && ts < 0 { q-- }`。

## 2. 边界归属

桶为左闭右开 `[b, b+step)`。点 `ts` 属于满足 `b <= ts < b+step` 的唯一桶；
恰好 `ts == b+step` 属于下一桶。每个桶在起点、起点前 1ns、终点三个位置逐一断言。

## 3. 乱序与逐位一致

桶内保留点，聚合前按时间戳排序（同戳按到达序稳定），因此：

- First/Last 由时间戳决定而非到达顺序；
- Mean 的求和按时间戳固定顺序进行，浮点结果可复现，用 `math.Float64bits` 比对；
- 同一批点打乱 20 次喂入，输出与排序喂入逐位相同。

## 4. 滑动窗口与资源约束

Aligner 只保留窗口内的桶；每来一个新桶索引 `maxIdx`，
吐出索引 `<= maxIdx-cap` 的已完成桶（按索引升序），Flush 吐出剩余桶。
驻留桶数峰值 <= cap（计数器原子记录历史峰值，测试用跨度 10 万桶、cap=100 断言）。
迟到点若落在已吐出桶（`idx < 已推进水位`）返回 `ErrLateDropped`，**不计入处理**；
被接受的点恰好计一次（`PointsProcessed == len(输入)`，cap 足够大时）。

## 5. 空洞语义

Aligner 吐出窗口扫过的每个桶索引（含空桶），由 gapfill 决定空桶去向：

- KeepMissing：空桶不出现在结果（不是 Count=0 记录）。
- CarryForward：空桶取前一个非空桶的 Last，Count=0；
  **序列开头的空桶没有前值，仍保持缺失**（绝不补零）。
- ZeroFill：空桶值为 0、Count=0。

## 6. NaN 与 Inf

NaN 无法参与有意义聚合：Push 拒绝（`ErrNaN`）并递增 `Skipped`，不进任何桶，
不污染 Mean。±Inf 是合法浮点值：正常参与，Min/Max/First/Last 可为 Inf，
且 Inf 参与求和时 Mean 变为 Inf（`Inf+(-Inf)=NaN` 的 IEEE754 规则同样适用）。

## 7. 落盘格式（小端，自描述）

```
header: magic "TSD1"(4) | version=1(1) | step int64(8) | originUnixNano int64(8) |
        agg uint8(1) | hdrCRC uint32(4)                       = 26 字节
record: start int64(8) | value float64(8) | count uint64(8) | recCRC uint32(4) = 28 字节
```

CRC32 均为 IEEE 多项式，覆盖各自 CRC 字段之前的字节。
截断长度 n 分类（L=26，R=28）：

- `n < L`：ErrTruncHeader（头部不完整，恢复 0 桶）。
- `L <= n < L+8`：ErrCRC（头部完整但落在 magic 与 hdrCRC 之间，头部 CRC 不匹配，0 桶）。
- `n == L`：完整无损前缀，无错误。
- `n = L+kR+o (k>=0, 0<o<8)`：ErrTruncRecord（第 k 条记录数据不完整，恢复 k 桶）。
- `o>=8 且 o<28`：ErrCRC（数据完整但 CRC 缺失/错，恢复 k+1 桶）。

三类错误可用 `errors.Is` 区分；恢复前缀按写入序，桶时间升序、无重复。
非法 step（<=0）构造即报 `ErrInvalidStep`；step 不整除跨度合法（末桶不满）。

## 8. 并发

Push 用单一互斥保护桶表与计数器；正确性只依赖聚合前排序，与到达先后无关，
因此多协程并发 Push 同一多重集的结果与串行一致（Mean 逐位），`-race` 干净。
