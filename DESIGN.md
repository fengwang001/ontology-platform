# 设计说明：带转义的 RLE 编解码器

不变量：严格解码器接受的每个 t 都满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 拒绝清单的逐条推导（若接受，则给出的 t 破坏不变量）

1. 显式次数 1：`1a` → Decode=`a` → Encode=`a` ≠ t。→ ErrCountOne
2. 次数 0：`0a` → Decode=`` → Encode=`` ≠ t。→ ErrCountZero
3. 前导零：`01a` → Decode=`a` → Encode=`a` ≠ t（单独的 `0` 归上一条）。→ ErrLeadingZero
4. 相邻同符号：`2a3a` → Decode=`aaaaa` → Encode=`5a` ≠ t；`aa` 同理（应为 `2a`）。→ ErrAdjacentSame，必须看到下一游程的符号才能判定
5. `\` 后非数字非反斜杠：`\a` 若按字面 `a` 接受，Encode=`a` ≠ t。→ ErrBadEscape
6. 末尾孤立 `\`：`a\` → Decode=`a` → Encode=`a` ≠ t。→ ErrTrailingEscape
7. 末尾只有次数：`a3` → Decode=`a` → Encode=`a` ≠ t。→ ErrMissingSymbol
8. 非法 UTF-8：无效字节只能解成 U+FFFD 或原样透传，Encode 输出 `�` 的编码 ≠ 原字节，不变量不可满足。→ ErrInvalidUTF8
9. 次数溢出 uint64：无法精确表示，任何截断都会让 Decode 输出与 t 不符。→ ErrCountTooLarge
10. 输出超上限：资源保护（见下），不属于格式非法。→ ErrOutputTooLarge

错误统一为 `*rle.Error{Kind, Off}`：Kind 为哨兵错误（`errors.Is` 可判定，彼此可区分），
Off 为字节偏移（取违规游程/符号/反斜杠的首字节；相邻同符号取后一个游程的首字节）。

## 大次数的取舍

选择**支持**大次数：上限 uint64。`runs.AddDigit` 逐位累加且先判溢出，绝不静默回绕；
超出 uint64 的次数拒绝（ErrCountTooLarge）。解码前不分配与次数成正比的内存：流式写出，
仅用 4KB 重复缓冲；输出总字节上限可配置（`NewDecoder` 的 maxOut，<0 表示不限；
`Decode` 默认 1<<30），在写出前检查 `n*len(sym)` 是否超剩余额度，超限即 ErrOutputTooLarge。

## 测试对照表

（待补）
