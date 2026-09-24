# DESIGN

## 第 3 条推导：只含空白的续入行

规则：物理行尾有奇数个 `\` 才续行（丢一个 `\`）；每个物理行拼入前先剥前导空白。
推论：若续入的物理行只含空白或为空，剥离后贡献空串；它又不可能以 `\` 结尾，
续行状态到此终止，逻辑行就此结束。例：`k=v\` 换行 `   ` 换行 `w` 得 `k`->`v`、`w`->``。
依据：`java.util.Properties.load` 按物理行末字符的 `\` 奇偶判定 continueLine，
全空白行末字符不是 `\` 故不再延续；前导空白剥离发生在拼接之前，全空白行被剥成
空串，即「接了等于没接」。结论：空白续行行是「续行首部空白被剥掉」的特例——
剥光后无贡献，且（因自身不带尾部反斜杠）终止续行；若写成 `   \` 则剥后剩 `\`，
仍为奇数，继续续行，两条规则自洽。

## 第 6 条推导：Store 的最小转义集

目标：`Load(Store(m)) == m`。Load 会剥行首空白、把空白/`=`/`:` 当分隔符、
把行首 `#`/`!` 当注释、把 `\` 当转义符。逐项推：
键：`\` 必转义（否则被当转义符）；所有空格必转义（空格是分隔符，键中空格会截断键）；
`=`、`:` 必转义（分隔符）；`#`/`!` 仅当键首字符时必转义（否则整行被当注释）；
`\t` `\n` `\r` `\f` 必转义（本身是空白或会断行）。
值：值从分隔符后第一个非空白字符起、到行尾逐字保留，故值中间与尾部的空格、
`=`、`:`、`#`、`!` 都不必转义；只有值首空格会被「分隔符后空白剥离」吃掉，
故仅首空格转义；`\` 与 `\t\n\r\f` 同理必转义。
`#`/`!` 开头的键必须转义，因为键位于逻辑行行首；值永远不在行首（前面必有键与
分隔符），故值不必。非 ASCII 原样以 UTF-8 输出：写出与读入都按字节透传，
往返一致，无需 `\uXXXX`。

## 语义 <-> 测试对照表

| 语义 | 钉住它的测试 |
| --- | --- |
| 1 分隔符与空白 | `props.TestLoad`（eq/colon/space/sep-ws-value-tail-kept/double-eq/empty-key/key-only/leading-ws） |
| 2 注释 | `props.TestLoad`（comment-hash/comment-bang/hash-in-value）、`logical.TestSplit/comment` |
| 3 续行（含空白续行） | `logical.TestSplit`（cont-odd/even/triple/hash/eof/blank-line/ws-backslash） |
| 4 转义与 `\u` 错误 | `props.TestLoad`（escapes/unicode/unknown-escape/escaped-*）、`props.TestBadUnicode` |
| 5 重复键顺序 | `props.TestLoad/dup-first-pos` |
| 6 回写最小转义 | `props.TestStore`（含逐条往返断言） |
| 7 随机往返 1000 组 | `props.TestRandomRoundtrip` |
| 计数器 <= 2n | `logical.TestCheckedLinear` |
