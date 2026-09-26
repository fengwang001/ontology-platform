# NOTES

## 三、编码分步表：BinOp("+", IntLit(258), Var("ab"))（共 18 字节）

| 字段 | 字节数 | 字节(hex) |
|---|---:|---|
| BinOp 标签 | 1 | 05 |
| 运算符 + 的枚举码 | 1 | 00 |
| 左子 IntLit 标签 | 1 | 00 |
| int64 258（小端） | 8 | 02 01 00 00 00 00 00 00 |
| 右子 Var 标签 | 1 | 02 |
| 名字长度 uint32（小端） | 4 | 02 00 00 00 |
| "ab" 原始字节 | 2 | 61 62 |
| 完整字节串 | 18 | 05 00 00 02 01 00 00 00 00 00 00 02 02 00 00 00 61 62 |

（甲）IntLit(2^32)：共 9 字节 = 标签 00 + 00 00 00 00 01 00 00 00，读回 4294967296；若误用 uint32 只写低 4 字节 00 00 00 00，读回被截断成 0。
（乙）Var("")：长度前缀为 0（00 00 00 00），名字 0 字节。若用 C 风格遇 0 停：空名字不产生内容，解码器会把下一节点首字节当成名字/终止符；下一节点恰为 IntLit（标签正好是 0x00）时会误读出空名并吞掉它的标签，之后整串错位。
（丙）Var("a\x00b")：长度前缀为 3（03 00 00 00），名字字节 61 00 62。若用 strlen 写长度只得 1，读回名字错成 "a"，剩余 0x00 被误当 IntLit 标签、0x62 等被误当 int64，整树错位。

## 二、四条不变量：保证位置 / 钉住的测试

1. 朴素参照一致：`codec/codec.go` 的 Encode 闭包 enc 与 decoder.decode 逐字段递归；`codec/codec_test.go` 内独立朴素参照 naiveEncode 逐字节比对——TestGolden258PlusAB、TestRoundTripNaiveReference。
2. 小端定宽：enc 用 binary.LittleEndian PutUint64/PutUint32，decode 用 must(8)/must(4) + Uint64/Uint32；IntLit 恒 9 字节、Var 前缀恒 4——TestEndiannessAndWidth。
3. 字符串按字节：decode 的 Var 分支 `string(d.b[d.pos:d.pos+m])` 原样切片复制，空字节/非 UTF-8/空串不做任何处理——TestVarRawBytes。
4. 失败不留痕：Decode 每次新建局部 decoder、无包级可变状态，失败只返回 nil 与哨兵（ErrTruncated/ErrIllegalTag/ErrIllegalBool/ErrVarTooLong 互不相同）——TestDecodeSentinelErrors、TestRejectionLeavesNoState。

附：边界定位 O(1) 由非导出字段 decoder.boundaryScans 见证（仅同包测试可读）——TestVarBoundaryScanBounded；并发 RoundTrip——`api/api_test.go` TestConcurrentRoundTrip。
