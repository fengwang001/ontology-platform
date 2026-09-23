# CSV 方言解析器设计

方言：逗号分隔；记录以 `\n` 或 `\r\n` 结束；引号字段内 `""` 转义为一个 `"`，
字段内的 `,` `\n` `\r\n` 原样保留（`\r\n` 不改写成 `\n`）。

## 1. 空行与单列空值

推导：`a,,b` 表示三个字段；但连续两个换行（`\n\n`）在 RFC4180 实践里是空行。
若把空行当作「含一个未引号空字段的记录」，则单列表中 `a\n\nb\n` 的空行就是
一条合法数据记录，用户无法区分「空行」与「真正的空值记录」。选择：**空行
（字段数为 0 的记录，含 `\r\n`）在 table 层跳过，不占记录号**。于是单列表
里空值记录只能写成 `""`。回写器规则：**列数为 1 的列中，值为空的字段必须
写成 `""`**；否则写空串再以换行收尾就成了空行，回读时被跳过，丢一条记录。
列数≥2 时空字段不写引号也可由逗号定位，故不加（最小引号）。

## 2. 事件模型与状态转移

lexer 对每段输入（给定「段首是否在引号内」假设）产出事件：
`open(b,quoted)` 字段开始；`data(b,e)` 内容片段（相对段基址）；
`close(e)` 字段结束；`rec(off)` 记录结束（换行符所在字节偏移，CRLF 时为 `\n`）；
`err(kind,off)`。偏移均为绝对字节偏移，段内坐标 + 段起始即全局坐标；
记录号/字段号由 table 的 builder 在最终事件流上编号。

状态：S0 字段开始；SU 未引号字段中；SQ 引号字段中；SQE 刚见引号（引号字段内）；
SCR 未引号字段末见 `\r` 待定。字节类：C 逗号；Q `"`；R `\r`；N `\n`；D 其他。

| 状态 | 输入 | 下一状态 / 动作 |
|---|---|---|
| S0 | D | SU；open(false)+data |
| S0 | Q | SQ；open(true) |
| S0 | C | S0；close+open(false) |
| S0 | R | SCR；close |
| S0 | N | S0；rec |
| SU | D | SU；data |
| SU | Q | err BareQuote |
| SU | C | S0；close+open |
| SU | R | SCR；close |
| SU | N | S0；close+rec |
| SQ | D,C,R,N | SQ；data（R/N 原样） |
| SQ | Q | SQE |
| SQE | Q | SQ；data(`"`)（`""` 计 1 字节） |
| SQE | C | S0；close+open |
| SQE | N | S0；close+rec |
| SQE | R | SCR'（已闭合，等同 SCR）；close |
| SQE | D | err CharsAfterQuote |
| SCR | N | S0；rec |
| SCR | 其他 | err BareCR（错误偏移指回 `\r`）；该 `\r` 不产生内容 |

EOF：S0 且本记录无字段→无事件（空尾/空行）；S0 有字段→close（末记录无换行）；
SU→close；SQ→err UnclosedQuote（偏移指开引号）；SQE→close；SCR→err BareCR。

## 3. 半包续传与 CR 待定

流式 lexer 只持有「当前状态 + 当前字段已收字节数 + 开引号偏移」，无输入回扫：
`\r` 进入 SCR 并立即 close 字段，但暂不发 rec；下一字节为 `\n` 发 rec，否则对
已暂存的 `\r` 报 BareCR。切分点落在 `\r`、`\n` 之间由 SCR 自然跨越；`""` 中间
由 SQ→SQE 跨越；流恰在 `\r` 结束由 Close 判 BareCR（CRLF 要求 `\n` 同流送达）。

## 4. par 切分与双假设

buf 按均匀字节界切成 K 段（K=1 时单段，切点可为任意偏移）。除第 1 段外，
每段在两种假设下各跑一次：段首在引号外（A）、段首在引号内（B，机器直接处于
SQ 且已有一个 quoted open）。段尾状态只取 {outside, inside, cr}。从左到右拼接：
已知前段尾状态即选定本段假设——若前段 inside 取 B，否则取 A；选定后得到本段
尾状态继续。故每段最多算两遍，总处理字节 ≤ 2N+O(K)，且与长引号字段长度无关。

拼接规则：前段 inside 时本段 B 的首个 open 与前段 open 合并（丢弃该 open，
其 data/close 接到前字段上），字段内容与引号标记因此逐段连续。

CR 落在切点：A 段以 SCR 收尾时已 close 未 rec；下一段首字节是 `\n` 则该段的
`\n` 只补一个 rec（段首 S0 的多余 open 在 close 前撤销），否则对该 `\r` 报
BareCR。错误偏移在段内已是绝对坐标（段基址+段内偏移）；记录号由 builder 在
拼好的全局事件流上统一编号，与单线程完全相同。

## 5. 上限

字段字节数在 lexer 逐字节累加（`""` 计 1，引号字段内容按原始字节累计），第一个
超限字节到达即发 err FieldTooLong，立刻进入终态（par 中错误事件同样终结拼接）。
字段数在第 MaxFields+1 个 open 处报 TooManyFields；记录数在第 MaxRecords+1 条
完整记录处报 TooManyRecords。终态后 Feed 返回同一错误。已产出记录保留。

## 6. 回写

必要引号：值含 `,` `"` `\r` `\n`，或第 1 节的单列空字段。引号字段写时整体加
`"`，内部 `"` 翻倍。记录间用 `\n`；末尾记录不补尾换行（规范输入定义：
`\n` 行尾、无空行、引号仅在必要处出现、末记录无尾换行）。

单个流式 Parser 实例非并发安全；多实例可并发使用。
