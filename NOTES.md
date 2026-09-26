# UTF-8 编解码：推导与不变量

## 第三节 八行分步表

| 字节序列 (hex) | 期望长度 | 逐步拼装 → 结果 |
|---|---|---|
| `41` | 1（`0xxxxxxx`） | 0x41 → **U+0041** |
| `C2 A2` | 2（`110xxxxx`） | (0x02<<6)\|0x22 → **U+00A2** |
| `C0 AF` | 2 | (0x00<<6)\|0x2F = U+002F < U+0080 → **非法：过长编码** |
| `E2 82 AC` | 3（`1110xxxx`） | (0x2<<12)\|(0x02<<6)\|0x2C → **U+20AC** |
| `ED A0 80` | 3 | (0xD<<12)\|(0x20<<6)\|0x00 = U+D800 → **非法：代理项** |
| `F0 9F 98 80` | 4（`11110xxx`） | (0x0<<18)\|(0x1F<<12)\|(0x18<<6)\|0x00 → **U+1F600** |
| `F4 8F BF BF` | 4 | (0x4<<18)\|(0x0F<<12)\|(0x3F<<6)\|0x3F → **U+10FFFF** |
| `F4 90 80 80` | 4 | (0x4<<18)\|(0x10<<12) = U+110000 > U+10FFFF → **非法：越界** |

## 三问

- (甲) `C0 AF` 实际表示 **U+002F**，正确的 1 字节形式是 **`0x2F`**。不查过长编码会解出 U+002F（白白消耗 2 字节）；正确实现应返回哨兵错误 `ErrOverlong`。
- (乙) 不查代理项范围会把 **U+D800** 当作合法码点照常返回；正确实现应返回哨兵错误 `ErrSurrogate`。
- (丙) `0x28` 的低 6 位是 0x28，并入得 (0x2<<12)|(0x28<<6)|0x2C = **U+2A2C**；正确实现应返回哨兵错误 `ErrBadContinuation`。

## 四条不变量落点

1. 往返一致：`enc.EncodeRune` 按码点范围分段编码、`enc.DecodeRune` 按同一张长度表解码并回拼；由 `TestRoundTrip`、`TestSelfCheck` 钉住。
2. 严格拒绝：`enc.DecodeRune` 内六类检查各配一个互不相同的哨兵错误；由 `TestDecodeVectors`、`TestFaultInjectionsCursorStable` 钉住。
3. 与朴素参照一致：`TestEncodeMatchesNaive` 内放手写教科书参照实现，全部向量逐字节比对。
4. 失败不留痕：`stream.Reader.Next` 仅在 `DecodeRune` 成功后才推进 `pos`；由 `TestFaultInjectionsCursorStable` 钉住。
