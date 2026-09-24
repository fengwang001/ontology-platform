# DESIGN

## 空白续行（语义第 3 条）的推导

- 依据 `java.util.Properties` 的 `LineReader.readLine`：行尾奇数个反斜杠触发续行时，
  该反斜杠被吃掉，并置 `skipWhiteSpace`/`appendedLineBegin`，下一物理行的前导空白被跳过。
- 若接上的物理行只含空白或为空：前导空白被剥掉后它不贡献任何字符；随后读到它的
  行尾符时，前面没有奇数个反斜杠，于是逻辑行**就此结束**。
- 结论：`k=v\` 换行后接全空白行 → `k`→`v`，该空白行是一个"空接续段"并终止逻辑行；
  除非它自身又以奇数个反斜杠结尾（如 `  \`），续行链才继续。
- 这正是"续行首部空白被剥掉"的直接推论：剥离使该行退化为空段，空段不提供新的
  续行反斜杠，于是行尾即逻辑行终点（对照 JDK 行为一致）。

## 回写转义（语义第 6 条）的推导

- 键：解析器在第一个未转义的 空格/`\t`/`\f`/`=`/`:` 处结束键，故这些字符在键中
  必须全部转义；行首非空白字符为 `#`/`!` 时整行变注释，故**键首**的 `#`/`!` 必须
  转义（键中间的 `#`/`!` 无此风险，不转义）；`\` 必须翻倍；`\n` `\r` 会断行，必须转义。
- 值：解析器只在分隔符之后、值开始之前跳过一次空白，此后到行尾逐字保留（所以
  load 时值尾空白不丢）。故值**中间和结尾**的空格不必转义；但值**开头**的空白会被
  那次跳过吃掉，必须转义。`\` 翻倍（行尾落单还会误触发续行）；`\n` `\r` 断行，转义。
- `#`/`!` 的注释判定只发生在逻辑行首字符，值永远到不了行首，故值中 `#`/`!` 不必转义。
- 非 ASCII：解析按字节透传，UTF-8 原样写出即可往返，无需 `\uXXXX`。

## 语义 ↔ 测试对照

| # | 语义 | 钉住它的测试 |
|---|------|--------------|
| 1 | 三种分隔符、分隔符两侧空白被吃掉、值尾空白保留、空键、仅有键 | `TestParseTable`（sep_*、double_equal、empty_key、key_only、leading_ws_ignored） |
| 2 | `#`/`!` 行首注释、行中 `#` 不是注释 | `TestParseTable`（comment_hash_and_bang、hash_inside_value） |
| 3 | 奇数反斜杠续行、续行首部空白剥离、偶数不续行、EOF 落单反斜杠丢弃、空白续行终止逻辑行 | `TestParseTable`（cont_*）与 `logical.TestLinesTable`；线性复杂度由 `logical.TestCheckedLinear` 钉住 |
| 4 | `\t\n\r\f`、`\uXXXX` 恰好 4 位十六进制、其他 `\x` 丢反斜杠、键内转义分隔符 | `TestParseTable`（escapes、unicode_escape_both_hex_cases、unknown_escapes、key_escaped_*）；错误行列号由 `TestBadUnicode` 钉住 |
| 5 | 重复键后者覆盖、遍历按首次出现顺序 | `TestParseTable`（dup_key_keeps_first_pos） |
| 6 | `Store` 最小转义回写、特殊字符逐项相等且顺序相同 | `TestStoreRoundTrip` |
| 7 | 1000 组随机映射 `Load(Store(m)) == m` | `TestRandomRoundTrip` |
