# RLE 设计推导

不变量：严格解码器接受的每个 t 必须满足 `Encode(Decode(t)) == t`（合法编码唯一）。

## 第 2 条：拒绝清单逐条推导

每类输入若被接受，给出反例 t 使不变量被破坏，故必须拒绝（错误均为独立哨兵，
包在 `*rle.Error{Kind, Off}` 中，Off 为出错构造的起始字节偏移）：

1. 显式次数 1：`Decode("1a")="a"`，`Encode("a")="a"` ≠ `"1a"` → `ErrCountOne`（Off=数字位）。
2. 次数 0：0 次游程不产出码点，编码器永不生成；`"0a"` 无论解成什么都无法 re-encode 回 `"0a"` → `ErrCountZero`。
3. 前导零：编码器写十进制无前导零，`Decode("01a")="a"`，re-encode 得 `"a"` ≠ `"01a"` → `ErrLeadingZero`。
4. 相邻同符号游程：编码器合并最长游程，`Decode("2a3a")="aaaaa"`，re-encode 得 `"5a"` ≠ `"2a3a"` → `ErrAdjacentSame`（Off=第二个游程首字节）。
5. `\` 后非数字非反斜杠：编码器只转义数字与 `\`，永不产生 `"\a"`；若解为 `"a"` 则 re-encode 得 `"a"` ≠ `"\a"` → `ErrBadEscape`（Off=反斜杠位）。
6. 末尾孤立 `\`：`\` 必须引出被转义符号，任何解码结果的 re-encode 都不会以 `\` 结尾 → `ErrTrailingEscape`。
7. 末尾只有次数无符号：次数必须修饰符号，re-encode 不可能以裸次数结尾 → `ErrMissingSymbol`（Off=次数首位）。
8. 非法 UTF-8：码点序列无法定义，Encode 只输出合法 UTF-8，re-encode 必不等于原字节串 → `ErrInvalidUTF8`（Off=非法字节）。

EOF 时校验顺序：不完整 UTF-8 > 孤立 `\` > 裸次数；次数取值（0/1/前导零）在符号到达时校验。

## 第 3 条：大次数的取舍

选择**拒绝超限次数**，理由：解码输出是「次数×符号字节数」的实体字节，超过输出预算的
次数无法物化，支持它没有意义。具体机制：

- `runs.Accum` 用 uint64 饱和累加解析次数（≥2^64 置饱和位），绝不回绕、不静默出错。
- 解码器有输出总字节上限（`DefaultMaxOutput`，可 `SetMaxOutput` 配置）；用
  `n > 剩余预算/符号长` 判定，不做可能溢出的乘法；超限在写出前返回 `ErrOutputTooLarge`
  （哨兵，可 `errors.Is` 判定），不按次数预分配内存。
- 例：`99999999999999999999a` 次数饱和，必超任何预算 → `ErrOutputTooLarge`。

## 测试对照表

（待补）
