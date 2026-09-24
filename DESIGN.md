# RLE 设计说明

不变量：严格解码接受的每个 t 都满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 必须拒绝的输入（逐条推导）

1. 显式次数 1（`1a`）：Decode 得 "a"，Encode 得 "a" ≠ "1a"。
2. 次数 0（`0a`）：空游程不是游程，且 Encode 永不产生 0；拒绝。
3. 前导零（`01a`、`00a`）：规范十进制无前导零；Encode 重写后 ≠ 原串。
4. 同符号相邻游程（`2a3a`）：Encode 必合并最长游程得 `5a` ≠ `2a3a`。
   判定要等下一游程符号确认，故错误偏移取下一游程起点。
5. `\` 后非数字非反斜杠（如 `\x`）：该符号的合法转义不存在，Encode 永不产生。
6. 末尾孤立 `\`：转义不完整；无对应符号，Encode 永不产生。
7. 末尾只有次数（`12`）：游程缺符号，无法满足“次数+符号”形状。
8. 非法 UTF-8：符号必须是 Unicode 码点；Encode 输出恒为合法 UTF-8，接受即破坏唯一性。
各类返回可判定哨兵错误（`ErrCountOne`…`ErrInvalidUTF8`），均包装为带字节偏移的错误。

## 大次数取舍

选择“支持但受输出上限约束”，拒绝静默溢出与提前分配。
- 次数用 `math/big` 逐位累加（runs 同时提供无溢出的 int64 解析），位数超 40 位即拒绝
  （远超 int64，且任何合法输出预算都装不下该游程）。
- 解码不按次数预分配；每次先比较 `count*UTF8Len(sym)` 与剩余预算，超限返回
  `ErrOutputLimit`，随后分块追加，内存与输出实际长度同阶。
- 预算可通过 `DecodeLimit`/`NewDecoderLimit` 配置，默认 `math.MaxInt64`。

## 语义—测试对照表

| 语义（第三节） | 测试函数 |
| --- | --- |
| 1 往返 | `TestRoundTrip` |
| 2 规范形式/严格拒绝 | `TestRejectTable` |
| 3 大次数 | `TestHugeCount` |
| 4 最长游程/不规范化 | `TestLongestRuns` |
| 5 跨切分点一致 | `TestStreamSplits` |
| 复杂度 字节计数 | `TestInspectCount` |
