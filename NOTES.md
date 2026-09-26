# 字节序编解码：推导与不变量

## 第三节推导：字节序列 `01 02 03 04 05 06 07 08`

| 宽度 | 字节序 | 取用字节 | 解出的值 (hex) |
|---|---|---|---|
| uint16 | 小端 | 01 02 | 0x0201 |
| uint16 | 大端 | 01 02 | 0x0102 |
| uint32 | 小端 | 01 02 03 04 | 0x04030201 |
| uint32 | 大端 | 01 02 03 04 | 0x01020304 |
| uint64 | 小端 | 01..08 | 0x0807060504030201 |
| uint64 | 大端 | 01..08 | 0x0102030405060708 |

- (甲) `0x01020304` 按大端写 uint32 → `01 02 03 04`；误用小端（或 LE 机器上写本机序）→ `04 03 02 01`。
- (乙) 首字段误当 uint64 小端读 8 字节 → `0x0000000200000001` = 8589934593；正确按 uint32 小端读 = 1。
- (丙) `FF FF FF FF` 无符号 uint32 = 4294967295；有符号 int32 符号扩展 = -1。

## 四条不变量（位置 / 钉住它的测试）

1. 往返一致：`codec.Decode` 按同一 order/width 调 `ord.Int*` 读回，`Encode` 用 `ord.PutInt*` 写；测试 `TestRoundTrip`。
2. 序互逆：`ord.Swap*` 用 `math/bits.ReverseBytes*`；BE 写 LE 读 = Swap；测试 `TestSwapTwice`、`TestCrossOrderIsSwap`。
3. 与朴素参照一致：`ord` 用 `encoding/binary`，测试内手写逐字节移位/反转参照；测试 `TestNaiveReference`。
4. 失败不留痕：`codec.Decode` 先校验 width 与长度对齐再分配，拒绝时返回 `(nil, err)`；测试 `TestRejectNoPartial`。
