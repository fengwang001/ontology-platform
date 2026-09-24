# 设计说明：带转义的文本 RLE

唯一合法编码不变量：严格解码器接受的每个 t 都满足 `Encode(Decode(t)) == t`。

## 必须拒绝的输入（逐条推导）

| 情形 | 接受后违反不变量的 t | 结论 |
|---|---|---|
| 显式次数 1，如 `1a` | `Decode`→`a`，`Encode(a)`→`a` ≠ `1a` | 拒绝 ErrCountOne |
| 次数 0，如 `0a` | `Decode`→`aa`，`Encode(aa)`→`2a` ≠ `0a` | 拒绝 ErrCountZero |
| 前导零，如 `02a` | `Decode`→`aa`，`Encode(aa)`→`2a` ≠ `02a` | 拒绝 ErrLeadingZero |
| 相邻同符号游程，如 `2a3a` | `Decode`→`aaaaa`，`Encode`→`5a` ≠ `2a3a` | 拒绝 ErrAdjacentSame |
| `\` 后非数字/反斜杠，如 `\a` | 该转义无对应码点，无法解码也无法重编码 | 拒绝 ErrBadEscape |
| 末尾孤立 `\` | 符号缺失，不是完整游程 | 拒绝 ErrTrailingBackslash |
| 末尾只有次数，如 `12` | 缺少符号，不是完整游程 | 拒绝 ErrMissingSymbol |
| 非法 UTF-8 符号字节 | Decode 只产出合法码点，无法无损重编码回原字节 | 拒绝 ErrInvalidUTF8 |

所有错误均为可判定哨兵，包进 `DecodeError`（字段 `Err`、0 基字节偏移 `Offset`）。

## 大次数取舍（第 3 条）

选择支持任意大次数：数字逐位累加进 `math/big.Int`，永不整数溢出，且不按次数
预分配内存。展开游程受可配置输出总字节上限约束（默认 64 MiB：`DecodeLimit`、
`Decoder.SetLimit`），分块追加；超限返回 ErrOutputLimit。Encode 侧次数来自输入
长度（≤ int），缓冲同样按需增长。

## 语义—测试对照表

| 语义 | 钉住它的测试函数 |
|---|---|
| 1 往返 | （收尾回填） |
| 2 规范形式/严格解码 | （收尾回填） |
| 3 大次数 | （收尾回填） |
| 4 最长游程/不规范化 | （收尾回填） |
| 5 跨切分点一致 | （收尾回填） |
| 计数器（第四节） | （收尾回填） |
