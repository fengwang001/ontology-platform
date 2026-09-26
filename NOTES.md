# NOTES

## 第三节推导：五字段消息分步表

| # | 字段号 / wire type / 值 | 键字节 | 值字节 |
| 1 | 1 / 0 varint / 150 | `08` | `96 01` |
| 2 | 2 / 2 length-delimited / "A"(41) | `12` | `01 41` |
| 3 | 3 / 5 fixed32 / 0x01020304 | `1D` | `04 03 02 01` |
| 4 | 4 / 0 zigzag32 / -1 | `20` | `01` |
| 5 | 5 / 1 fixed64 / 0x0807060504030201 | `29` | `01 02 03 04 05 06 07 08` |

整条消息（22 字节）：
`08 96 01 12 01 41 1D 04 03 02 01 20 01 29 01 02 03 04 05 06 07 08`

- (甲) `20 01`：u=1，正确 `Unzigzag=(1>>1) ^ -(1&1) = 0 ^ -1 = -1`；只做 `u>>1` 漏掉符号折叠得 **0**。
- (乙) 10 个 `FF`：第 10 字节低 7 位为 0x7F（>1，值超出 uint64）且续位仍为 1（需第 11 字节），正确实现返回 **enc.ErrOverflow**；naive 累进并丢弃超位得 `0xFFFFFFFFFFFFFFFF` = **18446744073709551615**。
- (丙) 键 varint=797：`797&7=5` fixed32 跳 4 字节，正确得字段 99=fixed32 字节 01 02 03 04（LE=**0x04030201**）、字段 2="A"；掩码误写 `&0x03`：797&3=**1** fixed64 需跳 **8** 字节，键后仅剩 7 字节 → **enc.ErrTruncated**，整串解码失败。

## 第二节四条不变量（保证位置 / 钉住测试）

1. **往返一致**：`wire.Encode`（wire/wire.go）按 wire type 逐字段原值拼装，`wire.Decode` 同规则还原 U/B；由 `api/api_test.go::TestAPIRandomRoundTrip` 与 `wire/wire_test.go::TestRandomRoundTrip` 钉住。
2. **顺序无关**：`wire.Decode` 单遍解析、直接写 `map[int]Field`，结果不依赖字段到达顺序；`wire/wire_test.go::TestDecodeOrderIndependent`（升序/降序字节串 DeepEqual）钉住。
3. **与朴素参照字节级一致**：`wire.Encode` 按字段号排序后键=`num<<3|wire`，varint/zigzag/fixed 全部经 `enc` 教科书式拼字节；`wire/wire_test.go::TestGoldenFiveFields` 逐字节比对硬编码 22 字节。
4. **失败不留痕**：`wire.Decode` 所有错误路径（截断/溢出/未知 wire/重复/字段号 0）一律 `return nil, err`，成功结束才返回局部 map；`wire/wire_test.go::TestRejectedDecodeNoPartial`（5 类哨兵互不相同 + 返回 nil + 失败后 golden 仍可正常解）钉住。
