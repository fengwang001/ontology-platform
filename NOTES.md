# NOTES

固定 schema、字段紧密排布无填充、小端。

| 字段  | 偏移 | 宽度 | 值          | 小端字节      |
| ----- | ---- | ---- | ----------- | ------------- |
| id    | 0    | 2    | 0x1234      | 34 12         |
| flags | 2    | 1    | 0xAB        | AB            |
| count | 3    | 4    | 0xDEADBEEF  | EF BE AD DE   |
| score | 7    | 2    | -1          | FF FF         |

整条 9 字节记录：`34 12 AB EF BE AD DE FF FF`

(甲) 错把偏移算成「前面字段个数」：count 是第 3 个字段 → 偏移 2；读 [2,6) 字节 `AB EF BE AD` 按小端解出 **0xADBEEFAB**（错值）。正确偏移 3 读出 `EF BE AD DE` = **0xDEADBEEF**。
(乙) `0x1DEADBEEF` 是 33 位、超出 uint32 值域：正确实现返回 `(nil, ErrValueOutOfRange)`，一个字节都不写；naive 静默截断低 32 位，写出 `EF BE AD DE`。
(丙) `FF FF` 误当 uint16 读得 **65535**；按 int16 符号扩展正确解为 **-1**。

不变量（保证位置 / 钉住的测试函数）：

1. 往返一致：`pack/pack.go` 的 `Pack` 逐偏移写小端、`Unpack` 按宽度符号扩展；`TestRoundTrip`。
2. 偏移自洽、区间不重叠不空洞全覆盖：`layout/layout.go` 的 `NewSchema` 累加宽度推导偏移；`TestOffsetsPacked`。
3. 与朴素参照字节级一致：`pack/pack.go` 的 `writeLE` 按偏移逐字节写；`TestNaiveReference`（内置参照实现逐字节对拍）。
4. 失败不留痕：`pack/pack.go` 的 `Pack` 先全量校验通过后才分配缓冲，三条失败路径均返回 nil；`TestRejectionsAtomic`。

另：O(1) 查找计数器为非导出字段 `probeCount`（`layout/layout.go`），白盒测试 `TestLookupConstantProbes` 直接读；并发由 `TestConcurrentPackUnpack` 钉住；`SelfCheck` 在 `api/api.go`，由 `TestSelfCheck` 钉住。
