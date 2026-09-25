# RLE 编解码器设计

不变量：严格解码器接受的每个 t 必须满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 拒绝清单逐条推导

1. 显式次数 1（`1a`）：若接受，Decode 得 `a`，而 `Encode("a")="a" != "1a"`。→ `ErrExplicitOne`，偏移=次数首数字。
2. 次数 0（`0a`）：0 次游程解码为不含该符号的串，如 `""`，`Encode("")="" != "0a"`；任何其他解释都自相矛盾。→ `ErrZeroCount`，偏移=次数首数字。
3. 前导零（`01a`、`007a`）：Decode 得 `a`/`aaaaaaa`，Encode 产出无前导零形式 `a`/`7a`，不等于原串。→ `ErrLeadingZero`，偏移=次数首数字。单独的 `0` 归第 2 条。
4. 相邻同符号游程（`2a3a`、`aa`、`\1\1`）：Decode 得 `aaaaa`，Encode 必合并为最长游程 `5a`，不等于原串。→ `ErrAdjacentSame`，偏移=后一个游程的首字节。
5. `\` 后非数字非反斜杠（`\a`）：Encode 只在数字和反斜杠前写 `\`，裸 `\a` 永不出现；任何解码结果再编码都不会带回这个 `\`。→ `ErrBadEscape`，偏移=`\` 之后那个字节。
6. 末尾孤立 `\`（`a\`）：同第 5 条，且转义缺少对象。→ `ErrTrailingBackslash`，偏移=该 `\`。
7. 末尾只有次数没有符号（`2`、`3ab12`）：游程 = 次数 + 符号，Encode 永不产出悬空次数。→ `ErrMissingSymbol`，偏移=次数首数字；此时游程不完整，不再细分 0/1/前导零。
8. 非法 UTF-8（含截断的多字节序列）：符号是 Unicode 码点，Encode 输出必为合法 UTF-8，非法字节不对应任何码点。→ `ErrInvalidUTF8`，偏移=出错 rune 的首字节。

## 大次数的取舍

选择**支持**任意大次数：`runs.ParseCount` 用 `math/big.Int` 解析，无溢出。防线在输出侧：`Decoder.MaxOutput`（默认 1<<30）限制解码输出总字节；写入前先比较 `n*RuneLen` 与剩余额度，超限（含次数超过 uint64）返回 `ErrTooLarge`，绝不按次数预分配内存，故 `99999999999999999999a` 安全报错而非崩溃。

## 语义 ↔ 测试对照

（待补）
