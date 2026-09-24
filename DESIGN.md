# RLE 设计推导

核心不变量：严格解码器接受的任意 `t` 都必须满足 `Encode(Decode(t)) == t`。
据此逐类推导必须拒绝的输入（`E` = `Encode(Decode(t))`）：

1. 显式次数 `1`（如 `1a`）：Decode 得 `a`，而 `E == "a" != "1a"`，非规范，拒绝 `ErrCountOne`。
2. 次数 `0`（`0a`）：Run 必须至少含 1 个码点；Decode 为空或产生空游程，`E` 不含该游程，拒绝 `ErrCountZero`。
3. 前导零（`03a`、`00a`）：规范编码无前导零；Decode 得 `aaa`，`E == "3a" != "03a"`，拒绝 `ErrLeadingZero`。
4. 相邻游程符号相同（`2a3a`）：编码须合并为最长游程；Decode 得 `aaaaa`，`E == "5a" != "2a3a"`。须缓冲一个游程、见到下一游程符号后才能判定，拒绝 `ErrAdjacentSame`。
5. 非法转义（`\x`）：转义只允许数字或 `\`；否则该符号无唯一合法解码，且规范编码不会产出它，拒绝 `ErrBadEscape`。
6. 末尾孤立 `\`：转义缺少被转义字符，不是任何 `Encode` 的输出，拒绝 `ErrDanglingBackslash`。
7. 末尾只有次数无符号（`12`）：游程必须以符号结尾；`E` 不可能以此结尾，拒绝 `ErrMissingSymbol`。
8. 非法 UTF-8（裸续字节、截断多字节序列、超长/代理编码）：解码符号必须是码点，规范输出恒为合法 UTF-8，拒绝 `ErrInvalidUTF8`。

大次数（第 3 条）取舍：**支持任意大小次数**，内部以 `math/big.Int` 保存，
十进制按位累加（总量由输入字节界定，无整数溢出，无静默错误）。
解码不按次数预分配：输出按 4KB 分块写入，写出前用 big.Int 比较
`次数×符号UTF-8长度` 与剩余输出预算；超过可配置上限（默认 64 MiB）返回
可判定的 `ErrOutputLimit`，故超大次数（如 100 位）会被预算拦截而非崩溃。

## 语义—测试对照表

| 第三节语义 | 钉住它的测试函数 |
| --- | --- |
| 1 往返（含数字/反斜杠/多字节/长游程） | `TestRoundTrip`（含随机用例） |
| 2 规范形式与八类严格拒绝（可区分错误+偏移） | `TestStrictRejects` |
| 3 大次数不溢出 + 输出上限可判定 | `TestHugeCountAndLimit` |
| 4 最长游程；组合字符不规范化 | `TestRunsSplit` / `TestRoundTrip` |
| 5 任意切分点逐位一致（含 `2a\|3a`） | `TestStreamingSplits` |
| 四 输入字节检查计数器 == 输入字节数 | `TestExaminedCount` |
