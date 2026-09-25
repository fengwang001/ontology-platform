# 设计：带转义的 RLE 编解码器

## 严格解码拒绝清单的推导（不变量：Encode(Decode(t)) == t，即合法编码唯一）

对每一类可疑输入，设解码器若接受它会得到 s；只要规范再编码 Encode(s) != t，
接受 t 就违反不变量，故必须拒绝。逐条：

- 显式次数 1：`1a` → s=`a`，Encode(s)=`a` ≠ `1a`。→ ErrExplicitOne
- 次数 0：`0a` → s=``，Encode(s)=`` ≠ `0a`。→ ErrZeroCount
- 前导零：`01a` → s=`a`，Encode(s)=`a` ≠ `01a`。→ ErrLeadingZero
- 相邻同符号：`2a3a` → s=`aaaaa`，Encode(s)=`5a` ≠ `2a3a`。→ ErrAdjacentSame
- `\` 后非数字非反斜杠：`\a` → s=`a`，Encode(s)=`a` ≠ `\a`。→ ErrBadEscape
- 末尾孤立 `\`：`a\` → s=`a`，Encode(s)=`a` ≠ `a\`。→ ErrLoneEscape
- 末尾只有次数：`12` → s=``，Encode(s)=`` ≠ `12`。→ ErrNoSymbol
- 非法 UTF-8：解码结果不是合法字符串，Encode 无定义，往返无从谈起。→ ErrBadUTF8

每类一个哨兵错误，包在 `*rle.Error{Off, Err}` 中带字节偏移，errors.Is/As 可判定。
充分性：不在上列的输入，其游程次数 ≥2 且无前导零、相邻游程符号互异、转义仅用于
数字与反斜杠、且为合法 UTF-8，故 Decode 后再 Encode 必逐字节等于自身。

## 大次数的取舍

选择「支持任意大的次数写法，但限制输出总量」：

- `runs.AddDigit` 把次数饱和在 `MaxCount = 2^60`，绝不整数溢出；
  饱和值再乘 UTFMax(4) 仍在 uint64 内，后续比较安全。
- 解码输出总字节上限可配置：`NewDecoder(max)`，`Decode` 用 `DefaultMaxOutput`；
  游程展开前先查 `次数*符号宽度 > 剩余额度`，超限返回 ErrTooLarge。
- 内存只随已展开的输出增长，从不在解码前按次数预分配。
- 例：`99999999999999999999a` 饱和后仍超限 → ErrTooLarge（偏移 0）。

## 语义 ↔ 测试对照表

（待测试落地后补齐）
