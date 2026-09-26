# NOTES

四字段推导（负载 / 长度 / 4字节小端前缀 / 完整字段）：

| 负载 | L | 前缀(hex) | 完整字段(hex) |
|---|---|---|---|
| `"hi"` | 2 | `02 00 00 00` | `02 00 00 00 68 69` |
| `""` | 0 | `00 00 00 00` | `00 00 00 00` |
| `"world!"` | 6 | `06 00 00 00` | `06 00 00 00 77 6f 72 6c 64 21` |
| `"A"` | 1 | `01 00 00 00` | `01 00 00 00 41` |

整条记录(25 字节)：`02 00 00 00 68 69 00 00 00 00 06 00 00 00 77 6f 72 6c 64 21 01 00 00 00 41`

- (甲) 空字段 = 4 字节前缀 `00 00 00 00`（值 0）+ 0 字节负载。若把长度 0 当非法：整条记录被拒、返回 `nil`；若解码漏掉空字段：4 字段变 3 字段，后续字段全部错位，往返不一致。
- (乙) 应返回 `(nil, ErrTruncated)`，整体失败、无部分字段；naive 不校验就按 100 切片 → 运行时 slice bounds 越界 panic（或读到缓冲外垃圾）。
- (丙) 正确：第 2 字段从 `0+4+2=` 偏移 6 开始，读到 `00 00 00 00` = 0。漏加前缀宽：从偏移 2 读 `[00 00 68 69]` = `0x69680000` = 1768423424，远超剩余 19 字节 → `ErrTruncated`。

## 四条不变量（保证位置 / 钉住测试）

1. 往返一致（含空字段）：`frame.Decode` 偏移循环，L=0 时照常取零长度负载入列；`api` 透传。测试 `TestRoundTrip`。
2. 前缀自洽、无重叠无空洞全覆盖：`Reader` 每次 `pos += 4+L` 直至 `pos == len(buf)`，入列前校验 `L ≤ 剩余字节`。测试 `TestPrefixSelfConsistent`。
3. 与朴素参照字节级一致：`frame.Encode` 只用 `lenp.PutLength`(PutUint32) + append 负载。测试 `TestEncodeMatchesReference`。
4. 失败不留痕：`Decode` 先校验后 append、失败即 `return nil`；`SkipField` 先校验后改 `pos`。测试 `TestAtomicFailure`、`TestSkipFailureNoAdvance`。

O(1) 跳过：计数器 `Reader.skipPayloadTouched` 为非导出字段，`SkipField` 只读 4 字节前缀、恒置 0；`TestSkipTouchesNoPayload`（m=100/1000/10000，负载 4096B）。并发：无包级可变状态，`TestConcurrentEncodeDecode` 钉住（`go test -race`）。
