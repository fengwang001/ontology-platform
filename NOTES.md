# NOTES — UTF-8 编解码推导与不变量落点

## 一、八个字节序列逐步解码表

| 字节序列 (hex) | 首字节期望长度 | 结果 |
|---|---|---|
| `41` | 1（`0xxxxxxx`） | U+0041 |
| `C2 A2` | 2（`110xxxxx`） | (0x02<<6)\|0x22 = U+00A2 |
| `C0 AF` | 2 | 非法：过长编码，解出 U+002F，本应 1 字节 |
| `E2 82 AC` | 3（`1110xxxx`） | (0x2<<12)\|(0x02<<6)\|0x2C = U+20AC |
| `ED A0 80` | 3 | 非法：代理项，(0xD<<12)\|(0x20<<6) = U+D800 |
| `F0 9F 98 80` | 4（`11110xxx`） | (0x1F<<12)\|(0x18<<6) = U+1F600 |
| `F4 8F BF BF` | 4 | 0x100000\|0xF000\|0xFC0\|0x3F = U+10FFFF（上限，合法） |
| `F4 90 80 80` | 4 | 非法：越界，0x100000\|0x10000 = U+110000 > U+10FFFF |

## 二、三问

- **(甲)** `C0 AF` 实际表示 U+002F，正确的 1 字节形式是 `2F`。不查过长编码的解码器会解出 U+002F（耗 2 字节，静默接受本可更短的写法）；正确实现应整体报错，返回哨兵错误 `ErrOverlong`。
- **(乙)** 不查代理项范围的解码器会把 U+D800 当作合法码点照常返回；正确实现应返回哨兵错误 `ErrSurrogate`。
- **(丙)** 不校验续字节前缀时，`28` 的低 6 位 0x28 被并进码点：(0x2<<12)\|(0x28<<6)\|0x2C = U+2A2C；正确实现应返回哨兵错误 `ErrBadContinuation`。

## 三、四条不变量的保证位置与钉住测试

1. **往返一致**：`enc/enc.go` 的 `EncodeRune`/`DecodeRune` 按同一套位布局互逆；测试 `TestRoundtrip`（api_test.go）。
2. **严格拒绝**：`enc/enc.go` 六个哨兵错误（ErrInvalidLead/ErrOverlong/ErrSurrogate/ErrOutOfRange/ErrTruncated/ErrBadContinuation）逐类判定；测试 `TestRejectTable`。
3. **与朴素参照一致**：测试内手写教科书参照 `refEncode`（api_test.go）逐段取位拼前缀；测试 `TestReferenceMatch`。
4. **失败不留痕**：`stream/stream.go` 的 `Next` 仅在 `DecodeRune` 成功后才推进 `pos`；测试 `TestCursorPinnedOnError`（stream_test.go）。

另：单趟线性由 `stream.DecodeAll` 只按 `DecodeRune` 返回的消耗字节数前进、从不重读保证，非导出计数器 `stats` 记录检查/回看字节数；测试 `TestSinglePass`。并发安全（无共享可变状态，计数器用 atomic）由 `TestConcurrent` 钉住；`api.SelfCheck` 由 `TestSelfCheck` 钉住。
