# DESIGN

## 1 状态机（lexer，逐字节，无回扫）

状态 S=FieldStart, U=Unquoted, Q=Quoted, A=QuoteSeen, C=CRSeen。字节类
COMMA `,` / QT `"` / NL `\n` / CR `\r` / OTH（其余）。位置随字节同步
增长，任何字节恰好处理一次（计数器 `bytesSeen` 只读）。

| 现态 | COMMA | QT | NL | CR | OTH |
|---|---|---|---|---|---|
| S | 发字段→U | →Q | 空行：不发记录 | →C | →U |
| U | 发字段→S | ErrBareQuote | 发字段+记录→S | →C | →U |
| Q | →Q | →A | →Q | →Q | →Q |
| A | 发字段→S | →Q(追加") | 发字段+记录→S | ErrBareCR | ErrAfterQuote |
| C | 发字段→U(逗号待发) | ErrBareCR | 发字段+记录→S | ErrBareCR | ErrBareCR |
| Close 在 U/S/A：发尾字段（空流不发）。在 Q：ErrUnclosedQuote，偏移=总长。在 C：ErrBareCR。

字段号在每次"发字段"时递增（含尾字段），记录号在记录结束时递增。错误位置：
ErrBareQuote 指向该 `"`；ErrAfterQuote 指向紧跟字符；ErrBareCR 指向 `\r`
（切分时上段末 `\r` 由下段首字节判决，Close 时由 EOF 判决）；列数不一致
（table 判）指向记录结束符。

## 2 空行与单列空值（推导）

若空行算作"一条含一个未引号空字段的记录"，则写入端无法表示它：单列记录的
唯一字段是未引号空值时写出的字节是 `\n`，与"空行"不可区分，再解析必被
跳过，往返失败。因此选择：**空行一律跳过**（仅 S 态见 NL/CRLF 才是空白行；
已见逗号则是有字段的记录，不跳过）。对称代价：**单列空值记录必须写作 `""`**
——引号"必要"的第五种情形，否则丢记录。必要引号共五类：值含 `,` `"` `\r`
`\n`，或它是单列表中唯一且为空的字段；引号转义 `""`。

## 3 CR 待定（推导）

U 末遇 `\r` 不能立即决定：后随 `\n` 是行尾（C→NL 结束），否则 ErrBareCR。
半包续传中 C 态原样挂起等下一字节；Close 时仍在 C 则报 ErrBareCR（偏移指向
该 CR）。切点落在 `\r\n` 间时，CR 归上段但判决依赖下段首字节：段间用 1 bit
`pendingCR` 传递（见 §4）。

## 4 par 切点：双假设（推导）

每段并行跑**两次完整模拟**：OUT 假设（段首在引号外，含跨段续接的未引号开放
字段）与 IN 假设（段首正在引号内），各记段内字段、结束快照（inQuote、
pendingCR、字段号、记录号）。段 0 起态已知（OUT, S）；之后从左到右用上段
结束快照 inQuote 选本段真实结果（true→IN，否则 OUT），总处理 ≤2N。字段在
`,`/换行才"发"，跨段开放字段由被选假设带至下段；段内偏移 start=base+relStart、
end=base+relEnd，错误加段 base；记录号/字段号为全局计数，随被选段链累加，
段首取进入时全局值。切点在 `""` 中：两假设分别视作"内容引号对"或"开/闭
引号"，唯一被链选出的假设恰与单线程一致。`\r` 在段尾：OUT 置 pendingCR 由
下段首字节判决；IN 中 CR 是内容不置位；自动正确处理 CRLF 跨切。上限"立刻"：
字段字节按**已接受逻辑字符字节数**计数，开引号不计，闭合 `""` 的第二引号
代表 1 个内容引号，在该字节计数并检查，超限立即 ErrFieldTooLarge，不缓冲整
记录；字段数每次发字段时检查 ErrTooManyFields；记录数在记录结束时检查
ErrTooManyRecords（已完成记录保留，解析器进入终态）。

## 5 规范输入（canonical）

LF 行尾；字段仅在 §2 五种情形加引号、`"` 转义为 `""`；无空行；末记录后恰有
一个 `\n`。对规范 x 有 Write(Parse(x))==x。
