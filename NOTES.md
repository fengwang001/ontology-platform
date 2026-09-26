# NOTES

## 一、推导（schema: id u16 @0, flags u8 @2, count u32 @3, score i16 @7，小端）

| 字段 | 偏移 | 宽度 | 值 | 小端字节 |
|---|---|---|---|---|
| id | 0 | 2 | 0x1234 | 34 12 |
| flags | 2 | 1 | 0xAB | AB |
| count | 3 | 4 | 0xDEADBEEF | EF BE AD DE |
| score | 7 | 2 | -1 | FF FF |

整条 9 字节记录：`34 12 AB EF BE AD DE FF FF`

- **(甲)** 按「字段个数」算偏移，count 是第 3 个字段 → 错放到偏移 2。从偏移 2 读 4 字节 `AB EF BE AD` 按小端解出 **0xADBEEFAB**；正确偏移 3 处是 `EF BE AD DE` → **0xDEADBEEF**。
- **(乙)** `0x1DEADBEEF` 为 33 位，超出 uint32 值域 → 正确实现返回 `(nil, ErrOverflow)` 整体失败；naive 静默截断低 32 位写出 `0xDEADBEEF`（字节 EF BE AD DE），把错误值落盘。
- **(丙)** `FF FF` 当 uint16 零扩展读出 **65535**；正确按 int16 符号扩展得 **-1**。

## 二、四条不变量的保证位置与钉住它们的测试

1. **往返一致**：pack.Pack 按宽度掩码写、Unpack 按符号/零扩展读（pack/pack.go），测试 `TestRoundTrip`（pack/pack_test.go）。
2. **偏移自洽**：layout.NewSchema 以前缀和推导偏移并校验宽度合法（layout/layout.go），测试 `TestOffsetsTightNoOverlap`（layout/layout_test.go）。
3. **与朴素参照一致**：测试内手写逐字节小端参照实现比对，测试 `TestMatchesNaiveReference`（pack/pack_test.go）。
4. **失败不留痕**：Pack/Unpack 先全量校验再写结果，出错返回 `(nil, err)`（pack/pack.go），测试 `TestFailuresAtomic`（pack/pack_test.go）。

并发安全：Schema 构建后只读，查找计数器用 atomic（layout/layout.go），测试 `TestConcurrentPackUnpack`、`TestLookupConstantComparisons`。
