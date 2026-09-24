# 设计说明：带转义的文本 RLE 编解码器

不变量：严格解码器接受的每个 t 必须满足 Encode(Decode(t)) == t（合法编码唯一）。

## 拒绝清单推导（每一类：若接受，哪个 t 违反不变量）

1. 显式次数 1：Decode("1a")="a"，而 Encode("a")="a" ≠ "1a"。拒绝（ErrExplicitOne）。
2. 次数 0：Decode("0a")=""（0 个 a），Encode("")="" ≠ "0a"。拒绝（ErrZeroCount）。
3. 前导零：Decode("01a")="a"，Encode("a")="a" ≠ "01a"。拒绝（ErrLeadingZero）。
4. 相邻同符号游程：Decode("2a3a")="aaaaa"，Encode("aaaaa")="5a" ≠ "2a3a"。
   同理裸写的 "aa" 也必须拒绝（应为 "2a"）。拒绝（ErrAdjacentSame）。
5. 反斜杠后非数字非反斜杠：Decode(`\x`)="x"，Encode("x")="x" ≠ `\x`。拒绝（ErrBadEscape）。
6. 末尾孤立反斜杠：转义缺少目标码点，没有任何明文 s 使 Encode(s) 以反斜杠结尾。拒绝（ErrTrailingBackslash）。
7. 末尾只有次数："2a3" 的 3 没有符号，没有任何明文以裸次数结尾。拒绝（ErrMissingSymbol）。
8. 非法 UTF-8：符号是 Unicode 码点，非法字节序列不对应任何码点，往返无定义。拒绝（ErrInvalidUTF8）。

每类是彼此可区分的哨兵错误，包在 *DecodeError 中并带输入字节偏移，偏移取「出错构造的
起始字节」：次数类取次数首位数字；相邻同符号取第二个游程的起点；转义类取反斜杠；
非法 UTF-8 取该码点首字节；输出超限取已消费字节数。

## 大次数的取舍：支持，不设次数上限

次数用 math/big.Int 解析（runs.ParseCount），不存在整数溢出。真正的风险是输出
爆炸，因此把上限放在解码输出总字节数上：DecodeLimit / NewDecoder 的 max
可配置，Decode 用 DefaultMaxOutput。冲刷游程前先用 big.Int 比较
「次数 × 符号字节数」与剩余额度，超限返回可判定的 ErrOutputTooLarge；写出由
runs.Expand 以不超过 8KiB 的块完成，任何时刻都不分配与次数成正比的内存。
故 99999999999999999999a 不溢出、不崩溃，稳定返回 ErrOutputTooLarge。

## 语义与测试对照

（测试对照表最后补齐）
