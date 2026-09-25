# DESIGN：半包续传 / 并行切分 CSV（RFC4180 方言）

## 1. 空行与单列空值（推导）

流末尾换行不产生记录，故解析器必须能识别"零字段的一行"。它有两种解释：跳过，或当成一条空记录。
多列表里空行无法解释为合法记录（列数必不符），跳过是唯一不产生噪声的选择；因此统一选择
**空行一律跳过**（`\n` 或 `\r\n` 落在字段起点且本记录尚未产出任何字段时抑制记录事件）。

后果：单列表里 `a\n\nb` 中间的空行被跳过；而单列表中"唯一字段为空"的真实记录（如
`a\n""\nb`）若不加引号回写，写出的就是空行，再解析被跳过 → 丢记录。故回写器的必要引号
除"含 `,`、`"`、`\r`、`\n`"外，还必须包括：**单列表中值为空的字段强制写成 `""`**。

规范输入定义：记录间仅用 `\n`，末记录无尾换行；字段仅在上述必要时加引号。对规范输入 x，
`Write(Parse(x)) == x`；任意 T，`Parse(Write(T)) == T`（字段值与引号标记逐位相同）。

## 2. par 任意切点（推导）

切点可落在引号字段甚至 `""` 中间，下一段 worker 无法自证是否在引号内，故每段按两种
假设各跑一遍同一状态机：**A=切点在引号外**；**B=切点在引号字段内**。B 的起始状态只看
切点前一个字节：是 `"` 则从"刚见引号"态起，否则从"引号字段中"起。正确性：真实在引号内
且前字节为 `"` 时，该 `"` 必是 `""` 转义对的首引号，从 quoteSeen 起，下一字节 `"` 正好
闭合转义；其余情况从 quoted 起，与单机推进等价。A 态下前字节为 `\r` 时从 sCR 起（挂起字段
取上一段尾片段），覆盖 CR 待定。拼接器自左向右维护真实模式（OUT/IN/CR），据此为每段选
A 或 B 的输出；OUT 段的首字段是开片段，与上段尾片段拼接（值直接相连、引号标记取或）。
沿真实链，每字节经历的状态/动作与单机逐字节完全一致，故拼接结果（含错误）相同。坐标不做
段内换算：选中的 atom 直接喂给全局 table.Builder，记录号/字段号由其全局计数，错误位置天然
是全局坐标（孤立 CR 错误的偏移取切点-1）。

## 3. CR 待定（推导）

裸字段中 `\r` 后随 `\n` 才算行尾，随其他字节（含 EOF）是孤立 CR 错误，故设 sCR：入态时
字段已定稿并挂起"记录待确认"；下一字节 `\n` 则落记录，否则 ErrLoneCR（指向该 `\r`）。
半包时 sCR 跨 Feed 保持，故 `\r` 与 `\n` 分两次喂结果不变；EOF 落在 sCR 报 ErrLoneCR。
par 切点在 `\r`/`\n` 之间时，下段按第 2 节 A 态 sCR 起步，行为同一状态机。
引号字段内 `\r` 是普通内容，永无待定。

## 4. 上限"立刻"（推导）

字段按**解码后字节数**计数：引号内每 append 1 字节计 1，`""` 仅在第二个引号处 append
一次即计 1。每次 append 前判断 `已有计数+1 > 上限`，超限即在该字节返回 ErrFieldTooLarge，
不缓冲整字段；错误偏移即该字节。字段数在每个字段定稿（逗号/行尾）时计数，超限立刻
ErrTooManyFields；记录数在每条记录落定时计数，超限立刻 ErrTooManyRecords。终态后 Feed
返回同一错误。

## 5. 状态转移表

状态：C=字段起点(sClear) B=裸字段中 Q=引号中 S=刚见引号 R=CR待定。
输入类：`,` `"` `\r` `\n` o=其他字节。emit=字段定稿；rec=记录落定；sup=空行抑制。

| 状态 | `,` | `"` | `\r` | `\n` | o |
|---|---|---|---|---|---|
| C | emit空(裸),C | Q | R(挂起) | rec或sup,C | append,B |
| B | emit,C | ErrBareQuote | emit后挂起,R | rec,C | append,B |
| Q | append,Q | append `"`,Q | append,Q | append,Q | append,Q |
| S | emit,C | append `"`,Q | emit后挂起,R | rec,C | ErrQuoteAfterClose |
| R | ErrLoneCR | ErrLoneCR | ErrLoneCR | rec,C | ErrLoneCR |

EOF：Q→ErrUnclosedQuote；R→ErrLoneCR；S/B/C 有已定稿或在写字段→rec（B 先 emit），无字段→空。
错误均为 ParseError{Kind, Byte(从0), Record, Field(从1)}：BareQuote/QuoteAfterClose/
UnclosedQuote/ColumnMismatch/LoneCR/FieldTooLarge/TooManyFields/TooManyRecords。
