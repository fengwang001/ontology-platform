# 设计说明：带转义的 RLE 编解码器

## 严格解码的拒绝清单（由不变量 Encode(Decode(t)) == t 推导）

合法编码唯一 ⇔ 严格解码器只接受 Encode 的像。逐条给出反例 t：

1. 显式次数 1：`1a` 解码得 "a"，而 Encode("a") = "a" ≠ "1a"。→ ErrExplicitOne
2. 次数 0：`0a` 解码得 ""，Encode("") = "" ≠ "0a"。→ ErrZeroCount
3. 前导零：`01a` 解码得 "a"，Encode("a") = "a" ≠ "01a"（次数写法不唯一）。→ ErrLeadingZero
4. 相邻同符号：`2a3a` 解码得 "aaaaa"，Encode 得 "5a" ≠ 原串（游程必须最长）。→ ErrAdjacentSame
5. `\` 后非数字非 `\`：编码器只转义数字与 `\`，`\q` 不在像中，无定义。→ ErrBadEscape
6. 末尾孤立 `\`：`a\` 的转义缺目标，不在像中。→ ErrTrailingEscape
7. 末尾只有次数：`12` 的游程缺符号，不在像中。→ ErrMissingSymbol
8. 非法 UTF-8：输出必须是合法 UTF-8 字符串，无法表示。→ ErrInvalidUTF8

每类错误是可 errors.Is 判定的哨兵，包在 *DecodeError 中带字节偏移。

## 大次数（第三节第 3 条）的取舍

选择**支持**任意大次数：runs.ParseCount 用 math/big 解析，绝不溢出。
解码器在写出任何字节前，用 big.Int 比较「次数 × 符号宽度」与剩余预算；
超过 maxOut（NewDecoder 参数；Decode 用 DefaultMaxOutput = 1 GiB）即返回
ErrOutputTooLarge，绝不按次数预分配内存。`99999999999999999999a` 在任何
合理预算下都在符号处被拒绝，而 `1000000a` 在预算内可正常流式输出。

## 实现要点

- runs：Split 切最长游程；AppendCount / ParseCount 负责次数的十进制读写。
- rle：单遍字节状态机（默认 / 读次数 / 读转义），每个输入字节恰好检查
  一次（inspected 计数器）；UTF-8 跨块用 pend 缓冲，故任意切分结果一致。
- 不做 Unicode 规范化：é 与 e+U+0301 是不同的码点序列。

## 测试对照表

（待测试写完后补齐）
