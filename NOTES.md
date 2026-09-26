# NOTES

## 第三节推导：五字段消息

| 字段号 | wire type | 键(hex) | 值(hex) |
|---|---|---|---|
| 1 | 0 varint | 08 | 96 01 (150=0b10010110 → 低7位0x16置续位=96, 高位0x01) |
| 2 | 2 length-delimited | 12 | 01 41 (长度1 + "A") |
| 3 | 5 fixed32 | 1D | 04 03 02 01 (0x01020304 小端) |
| 4 | 0 varint(zigzag32) | 20 | 01 (Zigzag32(-1)=(-2)^(-1)=1) |
| 5 | 1 fixed64 | 29 | 01 02 03 04 05 06 07 08 (0x0807060504030201 小端) |

整条消息: `08 96 01 12 01 41 1D 04 03 02 01 20 01 29 01 02 03 04 05 06 07 08`

- (甲) u=1, Unzigzag(1)=(1>>1)^-(1&1)=0^(-1)=**-1**；只做 `u>>1` 得 **0**（错）。
- (乙) 10 个 FF：每字节续位都为 1，第 10 字节后仍未终止 → 正确实现返回**溢出错误**。naive 累进 70 位、丢弃超出位，返回低 64 位全 1 = **0xFFFFFFFFFFFFFFFF (18446744073709551615)**。
- (丙) `key & 0x03`：wire 5 (0b101) 被读成 **1 (fixed64)**，跳过 **8** 字节（正确应跳 4）。`9D 06` 后仅剩 7 字节，跳 8 → **报截断错误**，字段 2 丢失。正确解析：字段 99 fixed32=0x04030201，字段 2 length-delimited="A"。

## 第二节四条不变量

1. 往返一致：`wire.Encode`/`wire.Decode` 互逆（enc 的 varint/zigzag/fixed 均互逆）；测试 `TestRoundTrip`。
2. 顺序无关：`wire.Decode` 按读到的键逐字段存入 `map[int]Field`，与字节序无关；测试 `TestOrderIndependent`。
3. 与朴素参照一致：`api.SelfCheck` 内置手写参照字节串逐字节比对 `Encode` 输出；测试 `TestNaiveReference`。
4. 失败不留痕：`wire.Decode` 出错即 `return nil, err`，结果 map 仅在全部解析成功后返回；测试 `TestFailureAtomic`。
