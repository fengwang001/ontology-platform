# RLE 设计推导

不变量：严格解码器接受的每个 t 必须满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 第 2 条：拒绝清单逐条推导

设解码器若接受某类 t，则 `Encode(Decode(t))` 必产生规范形式；只要它与 t 不同，接受即违反不变量：

- 显式次数 1（`1a`）：Decode→`a`，Encode→`a` ≠ `1a`。拒绝 `ErrExplicitOne`。
- 次数 0（`0a`）：0 个 a 是空串，Encode→`` ≠ `0a`；且规范次数 ≥ 2。拒绝 `ErrZeroCount`。
- 前导零（`01a`）：Decode→`a`，Encode→`a` ≠ `01a`。拒绝 `ErrLeadingZero`（`00a` 同属此类，先于零值判定）。
- 相邻同符号游程（`2a3a`、`aa`）：Decode→`aaaaa`，Encode 合并最长游程→`5a` ≠ `2a3a`。拒绝 `ErrAdjacentSame`。
- `\` 后非数字非反斜杠（`\a`）：符号 `a` 的规范写法是裸 `a`，Encode→`a` ≠ `\a`。拒绝 `ErrBadEscape`。
- 末尾孤立 `\`（`a\`）：`\` 不是任何游程的合法结尾，无对应符号，无法 Decode。拒绝 `ErrLoneBackslash`。
- 末尾只有次数（`2a3`）：次数后缺符号，游程不完整，无法 Decode。拒绝 `ErrMissingSymbol`。
- 非法 UTF-8：Encode 输出必为合法 UTF-8，故任何含非法字节的 t 都不可能是 Encode 的像。拒绝 `ErrInvalidUTF8`。

每类错误是可判定的哨兵错误（`errors.Is`），包装在 `*rle.Error` 中并携带字节偏移 `Off`。

## 第 3 条：大次数取舍

选择**支持任意大的次数字面量，但以可配置的输出字节上限兜底**：计数用饱和 uint64 累加（`runs.Count`），
不会整数溢出后静默出错；uint64 都装不下的次数必然超过任何 int64 上限，直接判 `ErrTooLarge`。
解码前只做 `n > (max-out)/符号宽度` 的除法比较，不分配与次数成正比的内存；
输出按 ≤4KB 块循环写出。`Decode` 用 `DefaultMaxOutput`，流式 `NewDecoder(w, maxOut)` 可配置。

## 测试对照表

（待补）
