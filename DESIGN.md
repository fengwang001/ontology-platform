# DESIGN

## 代理对错误偏移（语义 2）
高代理 H(D800–DBFF) 之后必须紧跟低代理 L(DC00–DFFF)。实现把 `\uH...` 视为“待决单元”：
H 已读完但下一个输入单元不是合法的 L（普通字节、`\u` 截断、引号结尾等），
错误一律报在 H 的起点（其反斜杠的偏移）；孤立低代理不开启待决单元，报在它自身起点。
理由：H 一旦读入，对前后文都不构成码点，把“配对所需的后续”计入定位时，
唯一自始至终已知的位置只有 H 的起点；继续吞字节判定等于猜测，与严格性矛盾。
`"\uD83DA"` 不能解释为“替换字符+A”：A 紧跟 H 已证明 H 无配对，
静默替换 U+FFFD 违反“不得静默替换”，且会让 `\uD83D` 与真实 U+FFFD 两种输入不可区分。

## 最小转义与唯一性（语义 4）
RFC 8259 只强制 `"`、`\`、U+0000–U+001F 必须转义。C0 控制区止于 0x1F，
U+007F(DEL) 不在其中，原样合法；`/` 的转义是可选故不转；U+2028/U+2029 为合法 UTF-8 原样输出。
映射：`"`→`\"`、`\`→`\\`，`\b \f \n \r \t` 用短形式，其余控制字符→`\u00XX`（小写四位）。
单射：每个源码点的输出词法单元互不相同；转义单元都以 `\` 开头，而未转义输出里不可能出现裸 `\`。
唯一：每个码点的形式无选择空间（短形式固定、hex 固定小写四位），故输出唯一，
最小转义字面量因此满足 Encode(Decode(y))==y。

## 测试对照（语义 → 测试函数）
| 语义 | 钉住它的测试函数 |
| --- | --- |
| 1 五类错误可区分且带偏移 | TestDecodeTable、TestErrorClassesDistinct |
| 2 代理对组合与拒绝偏移 | TestDecodeTable（grin/pair/lone-*/high-*/reversed）、TestEscTable |
| 3 非法 UTF-8 双向拒绝 | TestDecodeTable（badutf8-*）、TestEncodeTable（EncodeError 偏移） |
| 4 最小转义与原样输出 | TestEncodeTable |
| 5 往返与规范唯一 | TestRoundTripRandom |
| 6 任意切分逐位一致 | TestSplitConsistency |
| 复杂度 计数 ≤ 2n | TestExaminedBudget |
