# RLE 编解码设计

不变量：严格解码器接受的每个 t 都满足 `Encode(Decode(t)) == t`。

## 拒绝清单（违反不变量的全部形式）

1. 显式次数 1（`1a`）：解码得 `a`，再编码为 `a`≠`1a`。
2. 次数 0（`0a`）：次数必须 ≥1；再编码会变成空或别的形式，绝不可能等于 `0a`。
3. 前导零（`01a`、`00a`）：规范十进制无前导零；再编码为 `a` 或 `0a`-类非法形式，均不等于原文。
4. 相邻同符号游程（`2a3a`）：编码取最长游程，再编码为 `5a`≠`2a3a`（须看到下一符号才能判定）。
5. 非法转义（`\x`）：转义只允许数字与 `\`；`\x` 无合法再编码形式。
6. 末尾孤立 `\`：转义不完整，无符号可输出。
7. 末尾只有次数（`12`）：游程缺符号，无法再编码。
8. 非法 UTF-8：输出是合法 Go 字符串的 UTF-8 字节，非法序列不属于任何符号，再编码不可能重现它。
9. 次数溢出（见下）。

## 大次数取舍

选择**设上限拒绝**：`runs.MaxCount = math.MaxInt`（64 位平台 2^63-1）。
理由：次数是“重复该码点的个数”，而输出字节数和游程内迭代计数都要以机器整型表示；
接受更大的数在 64 位平台上无法物化为字符串，支持它没有可观测意义。
十进制累加 `n = n*10 + d` 时先检测 `n > (MaxCount-d)/10`，溢出立即返回 `ErrCountTooLarge`
（偏移为该数字处），因此绝不会静默回绕；`Write` 每字节只推进一次、拒绝前不展开，
故不会按次数预分配内存。另有可选输出总字节上限：`runs.WriteRepeated` 在写入每块前
先做不溢出的加法检查，超限返回 `ErrOutputLimit`，同样不成比例分配。

## 语义与测试对照

| 语义（第三节） | 钉住它的测试函数 |
| --- | --- |
| 1 往返 | rle：TestRoundTrip |
| 2 严格/规范 | rle：TestRejectTable、TestErrorsAreTyped、runs：TestReadCount |
| 3 大次数 | runs：TestReadCount、rle：TestHugeCount、TestOutputLimit |
| 4 最长游程/不规范化 | rle：TestEncodeTable、TestCombiningMarks |
| 5 跨切分点 | rle：TestAllSplits（含 2a3a）；计数 TestByteCounter |
