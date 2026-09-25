# RLE 设计说明

不变量：严格解码器接受的每个 t 必须满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 第 2 条：拒绝清单逐条推导（每条给出若接受则违反不变量的 t）

1. 显式次数 1（ErrCountOne）：接受 `1a` → Decode=`a`，而 Encode(`a`)=`a` ≠ `1a`。
2. 次数 0（ErrCountZero）：接受 `0a` → Decode=``，Encode(``)=`` ≠ `0a`。
3. 前导零（ErrLeadingZero）：接受 `01a` → Decode=`a`，Encode(`a`)=`a` ≠ `01a`。
4. 相邻同符号（ErrAdjacentSame）：接受 `2a3a` → Decode=`aaaaa`，Encode=`5a` ≠ `2a3a`；要看到下一游程符号才能判定，流式解码器保存上一符号即可，与切分无关。
5. `\` 后非数字非 `\`（ErrBadEscape）：接受 `\a`，无论解为 `a` 还是字面 `\a`，Encode 分别为 `a`、`\\a`，均 ≠ `\a`。
6. 末尾孤立 `\`（ErrLoneEscape）：Encode 输出中 `\` 只出现在转义对里，任何 s 的 Encode 都不会以裸 `\` 结尾，故 `a\` 不在值域内。
7. 末尾只有次数（ErrMissingSymbol）：Encode 输出中数字只作次数且后必有符号（数字符号必转义），故 `3`、`12` 不在值域内。
8. 非法 UTF-8（ErrInvalidUTF8）：符号是码点，Encode 只输出合法 UTF-8，含非法字节的 t 不在值域内。

EOF 冲突次序：先报次数值错误（`0`→ErrCountZero、`1`→ErrCountOne），再报 ErrMissingSymbol。每类错误是独立哨兵（errors.Is 可判定），并携带字节偏移（*rle.Error.Off）。

## 第 3 条：大次数的取舍

选择支持任意大次数（不拒绝），但解码输出字节上限可配置（NewDecoder 的 limit；Decode 用 DefaultLimit=16MiB），超限返回可判定的 ErrOutputLimit。次数逐位折叠进 uint64（runs.AddDigit），相对上限饱和，绝不溢出；展开按 ≤32KiB 块循环写出，不预分配与次数成正比的内存。

## 测试对照表

（待补）
