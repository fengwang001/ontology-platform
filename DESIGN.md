# 设计推导

不变量：严格解码器接受的每个 t 都满足 `Encode(Decode(t)) == t`（合法编码唯一）。
据此推导必须拒绝的输入（错误均携带字节偏移）：

- 显式次数 1（`1a`）：Decode 得 `a`，Encode 得 `a` != `1a` → ErrCountOne。
- 次数 0（`0a`）：空游程无符号；即便约定为空，Encode 不产生 `0a` → ErrCountZero。
- 前导零（`03a`）：3 的规范写法是 `3a`，重编码不等 → ErrCountLeadingZero。
- 相邻同符号游程（`2a3a`）：最长游程规则要求合并为 `5a`，重编码不等 → ErrAdjacentSame。
- 非法转义（`\x`）：转义只服务数字与反斜杠；原样重编码也不会出现 `\x` → ErrBadEscape。
- 末尾孤立 `\`：无法构成符号，且任何 Encode 输出不以裸 `\` 结尾 → ErrTrailingBackslash。
- 末尾只有次数（`12`）：游程缺符号，不可能是编码结果 → ErrTrailingCount。
- 非法 UTF-8：符号须为码点；合法输入是合法 UTF-8 → ErrInvalidUTF8。
- 次数 > MaxCount：见下，→ ErrCountTooLarge。

## 大次数取舍

选择设上限拒绝（ErrCountTooLarge），上限 `runs.MaxCount = math.MaxInt`：
- 一个游程序列化进 Go string 的输出码点数受 int 寻址限制，超过 MaxInt 的次数
  不可能存在于可往返的真实字符串中（Encode 永不产生该次数）。
- 拒绝它不损失任何合法往返：不存在 s 使 Encode(s) 含 >MaxInt 次数。
- 累加采用“乘 10 前先判溢出”：v > (MaxInt-d)/10 即报错，绝不静默溢出。
- 解码不按次数预分配；符号以分块（<=4096）循环写入，内存只随已产出字节增长。
- 总产出字节数受 Decoder 输出上限钳制（WithOutputLimit，默认 1<<30），
  超限返回 ErrOutputLimit；判定 `n > MaxInt/c` 在乘法前完成，防算术溢出。

## 测试对照表

| 语义（第三节） | 钉住它的测试函数 |
| --- | --- |
| 1 往返（含数字/反斜杠/多字节/长游程） | 待补 |
| 2 规范形式与严格拒绝（含偏移） | 待补 |
| 3 大次数/无溢出/输出上限 | 待补 |
| 4 最长游程/不做规范化 | 待补 |
| 5 跨切分点一致（含 2a|3a） | 待补 |
| 复杂度：检查计数==字节数 | 待补 |
