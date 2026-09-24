# 设计说明：带转义的 RLE 编解码器

## 严格解码的拒绝清单（由不变量 Encode(Decode(t)) == t 推导）

Encode 的输出必然满足：次数为 1 时省略；写出的次数 >= 2、无符号、无前导零；
游程最长（相邻两个游程的符号必不同）；只有 ASCII 数字与反斜杠被转义；
每个次数后紧跟一个符号；反斜杠后只跟数字或反斜杠；整体是合法 UTF-8。
因此任何不满足这些性质的 t 都不可能等于 Encode(Decode(t))，必须拒绝：

1. 显式次数 1：`1a` 解码得 `a`，而 `Encode("a")="a"` != `1a` → ErrCountOne
2. 次数 0：`0a` 解码得 ``，而 `Encode("")=""` != `0a` → ErrZeroCount
3. 前导零：`02a` 解码得 `aa`，而 `Encode("aa")="2a"` != `02a` → ErrLeadingZero
4. 相邻同符号：`2a3a` 解码得 `aaaaa`，而 `Encode("aaaaa")="5a"` != `2a3a` → ErrAdjacent
5. 非法转义：`\x` 解码得 `x`，而 `Encode("x")="x"` != `\x` → ErrBadEscape
6. 末尾孤立 `\`：Encode 输出中反斜杠只以 `\\`、`\数字` 成对出现，`a\` 不可生成 → ErrLoneEscape
7. 末尾只有次数：Encode 写出的每个次数后必有符号，`2`/`a2` 不可生成 → ErrBareCount
8. 非法 UTF-8：Encode 输出恒为合法 UTF-8，非法字节序列不可生成 → ErrInvalidUTF8

每类错误是可判定的哨兵错误（errors.Is），统一包在 *rle.Error 中并携带字节偏移 Off。

## 大次数的取舍

选择支持任意大次数：runs.ParseCount 用 math/big.Int 解析十进制，绝不溢出。
解码输出字节上限可配置（NewDecoder(limit)，Decode 使用 DefaultLimit=64MiB）；
发射前用 big.Int 比较 count×符号宽度与剩余额度，超限返回可判定的 ErrTooLarge，
绝不按次数预分配内存。`99999999999999999999a` 得到 ErrTooLarge 而非崩溃或错值。

## 语义与测试对照

（待测试完成后补齐）
