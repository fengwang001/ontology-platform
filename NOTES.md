# NOTES
## 1. zigzag 映射方向推导
两种自然写法（v 为 int64，u 为 uint64）：
| v | A: (v<<1)^(v>>63) | B: (uint64(v)<<1) 加符号位 |
|---|---|---|
| 0 | 0 | 0 |
| -1 | 1 | 18446744073709551615 |
| 1 | 2 | 2 |
| -2 | 3 | 18446744073709551613 |
| math.MinInt64 | 18446744073709551615 | 1 |

写法 B 把 -1、-2 映射到接近 2^64 的巨大值，varint 编码需 10
字节，违反"小值短编码"的设计目的，故采用写法 A（zz.Encode）。
A 在 MinInt64 上不溢出：Go 移位按定长位模式定义，MinInt64<<1=0，
MinInt64>>63=-1，uint64(-1) 为全 1，0 异或全 1 得 MaxUint64。

## 2. 四条不变量的保证位置与测试
1. 往返恒等：zz.Encode/zz.Decode 互为逆映射（zz/zz.go），
   codec 两个函数是其与 vint 的直接组合（codec/codec.go）；
   测试 TestRoundTrip。
2. 最短形式唯一：vint.Uvarint 拒绝"多字节且末字节为 0"的输入，
   返回 ErrNonMinimal（vint/vint.go）；测试 TestErrorKinds。
3. 切片往返与边界无关：codec.DecodeSlice 顺序消费缓冲区，任何
   截断只会得到合法前缀或 ErrIncomplete（codec/codec.go）；
   测试 TestSliceSplit。
4. 失败不留痕：DecodeSlice 出错即返回 nil 且从不写传入切片，
   错误用 %w 包装并带元素下标（codec/codec.go）；
   测试 TestFailureNoTrace。
