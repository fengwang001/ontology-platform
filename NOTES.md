# NOTES

## 1. Zigzag 映射方向推导（本题推导点）

两种自然的 int64 → uint64 双射写法：

- 写法A（zigzag）：`u = uint64(v)<<1 ^ uint64(v>>63)`，序列 0,-1,1,-2,2,… 映射为 0,1,2,3,4,…
- 写法B（补码直接重解释）：`u = uint64(v)`，小负数落在 uint64 高端。

五值对照表：

| 值        | 写法A 的 u              | 写法B 的 u              |
|-----------|-------------------------|-------------------------|
| 0         | 0                       | 0                       |
| -1        | 1                       | 18446744073709551615    |
| 1         | 2                       | 1                       |
| -2        | 3                       | 18446744073709551614    |
| MinInt64  | 18446744073709551615    | 9223372036854775808     |

写法B 下 -1（及所有小负数）映射到 uint64 顶端，编码长达 10 字节，违反"小值短编码"，故采用写法A。
MinInt64 = -2^63：`uint64(v)<<1` 按 2^64 取模回绕为 0，再异或 `v>>63`（全 1）得 2^64-1，
即 uint64 的最大值而非越界，不溢出；它恰好需要 10 字节，第 10 字节仅 1 个有效位。

## 2. 四条不变量：保证位置与钉住的测试

1. 往返恒等：`zz.go` Zig/Unzig 互逆且 `codec.go` EncodeInt/DecodeInt 组合；测试 `TestRoundTripBoundaries`。
2. 最短形式唯一：`vint.go` Uvarint 对"末字节 7 位全 0"判 ErrNonShortest（首字节 0x00 即一字节解出 0）；测试 `TestRejectNonShortest`。
3. 切片往返与任意切分：`codec.go` EncodeSlice/DecodeSlice，切分喂入只可能整体成功或 ErrIncomplete；测试 `TestSliceRoundTrip`、`TestSliceCutAnyByte`。
4. 失败不留痕：`codec.go` DecodeSlice 先在临时切片中解析、成功后才返回结果，错误信息带元素下标；测试 `TestFailureAtomic`。
