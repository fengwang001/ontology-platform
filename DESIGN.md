# 带转义的 RLE 编解码器设计

格式：游程 = 可选十进制次数 + 一个码点符号；码点为 ASCII 数字或 `\` 时必须
转义为 `\` 加该字符；次数 1 省略，次数 ≥ 2 写无前导零的十进制。

## 严格解码拒绝清单（由 Encode(Decode(t)) == t 逐条推导）

不变量：合法编码唯一，解码器接受的每个 t 都必须满足 Encode(Decode(t)) == t。
以下每类若被接受，都存在一个 t 违反该不变量，故必须拒绝：

1. 显式次数 1：`1a` 若接受则解出 "a"，而 Encode("a") = `a` ≠ `1a`。
2. 次数 0：`0a` 若接受则解出 ""，而 Encode("") = `` ≠ `0a`。
3. 前导零次数：`01a` 若接受则解出 "a"，而 Encode("a") = `a` ≠ `01a`。
4. 相邻游程同符号：`2a3a` 若接受则解出 "aaaaa"，而 Encode("aaaaa") = `5a`
   ≠ `2a3a`；同理裸写的 `aa` 也必须拒绝（应为 `2a`）。
5. `\` 后不是数字也不是反斜杠：`\a` 若把符号当作 "a"，Encode("a") = `a`
   ≠ `\a`；转义后只允许数字或 `\`，其余无合法解释。
6. 末尾孤立的 `\`：`a\` 的转义缺少被转义字符，任何解码结果的 Encode 都
   不会以 `\` 结尾，必然 ≠ `a\`。
7. 末尾只有次数没有符号：`12` 没有可重复的符号，任何输出的 Encode 都不
   是 `12`。
8. 非法 UTF-8：码点无法确定，且输出必须是合法 UTF-8 字符串，无法保证往返。

每类对应一个哨兵错误（ErrExplicitOne、ErrZeroCount、ErrLeadingZero、
ErrAdjacentRun、ErrBadEscape、ErrDanglingEscape、ErrMissingSymbol、
ErrInvalidUTF8），统一包在 *rle.Error{Err, Offset} 中携带字节偏移。
注：符号位上的裸数字恒被读作次数（如 `a1` 最终以 ErrMissingSymbol 收尾），
所以「未转义的数字/反斜杠作符号」天然不可能出现，无需单列。

## 大次数（第 3 条）的取舍

选择**支持**任意大次数：runs.Count 逐位累积十进制数字、用 math/big 求值，
不存在整数溢出；次数的内存占用与输入长度成正比，而非与次数值成正比。
解码不在解码前按次数预分配内存：输出按块写入，且总字节数受
Decoder.MaxBytes 限制（0 表示默认 1<<30），超限返回哨兵错误
ErrOutputLimit，可用 errors.Is 判定。

## 测试对照表

（待测试写完后补齐）
