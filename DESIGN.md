# RLE 编解码器设计

不变量：严格解码器接受的每个 t 必须满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 第 2 条：拒绝清单逐条推导

- 显式次数 1：`1a` 若被接受，Decode→`a`，Encode→`a` ≠ `1a`。→ ErrCountOne
- 次数 0：`0a` 若被接受，Decode→空串，Encode→空串 ≠ `0a`。→ ErrCountZero
- 前导零：`01a` 若被接受，Decode→`a`，Encode→`a` ≠ `01a`（`007x` 同理）。→ ErrLeadingZero
- 相邻同符号：`2a3a` 若被接受，Decode→`aaaaa`，Encode 合成最长游程得 `5a` ≠ `2a3a`。→ ErrAdjacentSame
- `\` 后非数字非反斜杠：`\a` 若按字面 `a` 接受，Encode→`a` ≠ `\a`；其他码点同理。→ ErrBadEscape
- 末尾孤立 `\`：`a\` 不构成任何符号；若忽略，Decode→`a`，Encode→`a` ≠ `a\`。→ ErrLoneEscape
- 末尾只有次数：`2a3` 的 `3` 无符号；若忽略，Decode→`aa`，Encode→`2a` ≠ `2a3`。→ ErrNoSymbol
- 非法 UTF-8：输出必须是合法 UTF-8 字符串；Encode 按码点工作，无法重新产生非法字节序列，等式不可能成立。→ ErrBadUTF8

每类错误是可判定的哨兵错误，包在 `*rle.Error` 中并带字节偏移（Offset 指向相关符号或游程起点）。

## 第 3 条：大次数的取舍

选择**支持**任意大的次数：次数用 `math/big.Int` 解析（`runs.ParseCount`），不存在整数溢出，
`99999999999999999999a` 是语法合法的游程。但解码输出受可配置上限约束
（`Decoder.MaxOutput`，0 表示默认 64 MiB）：游程完成时先以 big.Int 比较
`次数×符号字节数` 与剩余额度，超限返回可判定的 ErrTooLong，**不分配**与次数成正比的内存；
内存占用只与实际上限成正比。因此大次数被接受、被检查、被确定性地拒绝输出，而非静默溢出。

## 语义 ↔ 测试对照表

（待测试写完后补齐）
