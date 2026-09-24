# NOTES

## 1. zigzag 映射方向推导

- 写法 A（符号位异或到低位）：`u = uint64(v<<1) ^ uint64(v>>63)`
- 写法 B（数值直接左移、符号位置最低位）：`u = uint64(v<<1) | uint64(v>>63)&1`

| v | A | B |
|---|---|---|
| 0 | 0 | 0 |
| -1 | 1 | 2^64-1 |
| 1 | 2 | 2 |
| -2 | 3 | 2^64-3 |
| math.MinInt64 | 2^64-1 | 1 |

写法 B 下 `-1` 映射为 `2^64-1`，varint 需 10 字节，违反"小值短编码"的设计目的，故采用写法 A。
A 在 `math.MinInt64` 上不溢出：Go 的有符号移位与异或都是定义良好的位运算，`MinInt64<<1`
按二进制补码回绕为 0，`v>>63` 是算术右移得 -1（全 1 位模式），二者异或得 `2^64-1`，
解码时 `u>>1` 与 `-int64(u&1)` 异或精确还原，全程无未定义行为、无精度丢失。

## 2. 四条不变量的落实位置与钉住它们的测试

1. 往返恒等：`zz.Encode`/`zz.Decode` 互逆，`codec.EncodeInt`/`codec.DecodeInt` 组合二者；测试 `TestRoundtrip`。
2. 最短形式唯一：`vint.Uvarint` 在末字节为 0 且非首字节时返回 `ErrNonMinimal`；测试 `TestErrors`。
3. 切片往返与边界无关：`codec.DecodeSlice` 遇截断输入返回 `ErrIncomplete`，绝不编造值；测试 `TestSliceRoundtrip`、`TestSplit`。
4. 失败不留痕：`codec.DecodeSlice` 只读传入切片、出错时返回 nil 整体失败；测试 `TestNoMutation`。
