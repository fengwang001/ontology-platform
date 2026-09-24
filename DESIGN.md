# CSV 方言设计

## 推导与结论

1. **空行与单列空值**：取“空行被跳过”。两个连续换行之间没有字段，也无法承载引号信息；若把它当记录，文件尾换行会变成额外记录，违反末尾不变量。因此规范输入不含空行。单列表中真实记录的唯一空字段必须写 `""`；只写空内容再加换行与“空行”不可区分，读回会被跳过，故丢记录。多列空字段仍可写空串。
2. **并行切点**：先用一次不产出的线性状态机从左到右记录 K 个边界的完整状态（引号态、活动字段前缀、记录/字段号、字节游标）。这一遍每字节一次；随后 K 个 goroutine 各自从精确边界状态只扫描本段一次。活动字段的前缀随状态传入，不重扫历史字节；段间只按“本段结束的完整记录 + 末段尾记录”拼接。状态在数学上等价于单线程 Feed 到切点，因此记录序、引号判定和错误坐标相同；错误本就携带全局偏移与全局记录/字段号。
3. **CR 待定**：`crPending`只由引号外字段后的裸`\r`进入。半包时下一字节为`\n`则记录结束；为逗号则字段结束且逗号有效；其他字节为孤立 CR 错误，位置在 CR。EOF 时 CR 既不能形成行尾也不能保留为裸字段内容，同样报孤立 CR。引号内 CR 只是内容，若下一字符不是 LF 也原样保留。
4. **即时上限**：每接收一个会进入字段值的字节即递增字段长度：裸字段计入普通字节；引号内除首尾定界引号外，普通字节计一，`""`在第二个引号确认是转义后总共计一。递增后立即检查字段上限。字段数在逗号后、记录数在记录完成时立即检查。

规范输入：无空行；无末尾换行；引号仅用于包含 `,`、`"`、`\r`、`\n` 的字段，或单列表中的空字段；转义写 `""`，字段内 CRLF 原样。

## 状态转移

| 状态 | 字节类别 | 下一状态 / 动作 / 错误 |
|---|---|---|
| fieldStart | `,` | fieldStart；空裸字段结束 |
| fieldStart | `"` | inQuoted；字段为引号字段 |
| fieldStart | `\r` | crPending；暂存裸 CR |
| fieldStart | `\n` | fieldStart；空行跳过 |
| fieldStart | 其他 | inField；追加 |
| inField | `,` | fieldStart；裸字段结束 |
| inField | `\r` | crPending；暂存裸 CR |
| inField | `\n` | fieldStart；记录结束 |
| inField | `"` | 报裸字段引号 |
| inField | 其他 | inField；追加 |
| inQuoted | `"` | quotedQuote；可能闭合或转义 |
| inQuoted | 其他 | inQuoted；追加并即时计长，CR 原样 |
| quotedQuote | `"` | inQuoted；输出一个引号，转义共计一 |
| quotedQuote | `,` | fieldStart；引号字段结束后逗号 |
| quotedQuote | `\r` | crPending；记录可能 CRLF 结束 |
| quotedQuote | `\n` | fieldStart；记录 LF 结束 |
| quotedQuote | 其他 | 报闭合后非法字符 |
| crPending | `\n` | fieldStart；记录 CRLF 结束，内容不含换行 |
| crPending | `,` | fieldStart；字段结束后逗号 |
| crPending | 其他 | 报孤立 CR |
| crPending | EOF | 报孤立 CR |
| 其他引号态 EOF | EOF | inQuoted 报未闭合；其余刷新尾记录 |
