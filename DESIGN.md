# 设计推导（CSV 方言解析器）

## 1. 空行与单列空值
候选：跳过空行，或产出一条「单字段、未引号、空」记录。跳过会让"流里有几个换行"不可观察；
且单列表里 `a\n\nb\n` 若把空行跳过，则 `[a][][b]` 与 `[a][b]` 无法区分，丢数据。
故选**不跳过**：空行 = 一条含一个空 cell（quoted=false）的记录。
末尾换行不产生额外记录：最后一个 `\n` 关闭它前面的记录；其后若无字节，流即结束。
回写推导：单列、值为空的记录若不加引号，只能写成空串；而"空串 + 记录分隔符"正是空行，
解析回来仍是一条空记录，看似无事——但 `""\n`（加引号空）与 `\n`（不加）必须能区分；
若记录要表达"有一个明确的、加引号的空字段"，写空串会丢 quoted 标记。
故回写器：**quoted=true 必写 `""`；含 `,` `"` `\r` `\n` 必加引号**；单列未引号空值写空串（与空行同形，语义一致）。
规范输入：行分隔仅 `\n`（`\r\n` 仅出现在引号字段内）、只在上述必要情形加引号；`Write(Parse(x))==x`。

## 2. par 切点
每段在「起点在引号外 H0」「起点在引号内 H1」两种假设下各完整跑一遍状态机（含 cell 起止）。
真实状态从左到右确定：H0 假设下段内引号外的引号数量奇偶决定其段末是否仍在引号内；
H1 是 H0 的镜像。段 i 的真实起点 = 段 i-1 真实终点（外→H0，内→H1）。
双假设只各扫一遍，任何切点代价都是常数，不重扫长字段。
`\r\n` 被切：把待决 `\r` 作为 1 字节前缀并入下一段（该 worker 额外处理 ≤1 字节），仍常数。
拼接：每段产出 cell/行结束事件，跨段未闭合 cell 与下段首个 cell 合并（取真实起点的 quoted 标志）。
坐标换算：cell 偏移 = 段基址 + 段内偏移；记录号 = 跨段累计已闭合记录数 + 段内序号；字段号随 cell 累加。
错误在真实假设的事件流上按全局偏移排序，首个错误即单线程的首个错误，位置随上述换算全局化。

## 3. CR 待定
状态 CR：引号外见到 `\r`。次字节为 `\n` → 行结束（分隔符，回写为 `\n`）；
为其它字节或 EOF → ErrBareCR，偏移指向该 `\r`。半包：CR 是可序列化的挂起状态，无后续 Feed 即 Close 判错。
par：见第 2 条，待决 `\r` 以前缀字节传给下一段；恰为 buf 末字节则按 EOF 判错。引号内 `\r` 是内容，原样保留。

## 4. 上限的"立刻"
每接收一个内容字节即把字段长度 +1 并比较 MaxFieldBytes；`""` 转义只产出 1 个内容字节、计数 1，
故在第一个超限字节到达的当步返回 ErrFieldTooLong，不缓冲整字段。
MaxFieldsPerRecord 在 cell 闭合时计数；MaxRecords 在记录闭合时计数；超限即终态，错误粘滞。

## 5. 状态转移表（动作：a=append 内容, c=cell 闭合, r=记录闭合）
| 状态 | `,` | `"` | `\r` | `\n` | 其它 |
|---|---|---|---|---|---|
| FStart 字段开始 | c+新cell,→FStart | →Quoted | →CR | r,→FStart | a,→Bare |
| Bare 未引号字段 | c,→FStart | ErrQuoteInBare | →CR | r,→FStart | a,→Bare |
| Quoted 引号字段 | a | →QQuote | a | a | a |
| QQuote 见一引号 | c,→FStart | a(`"`),→Quoted | ErrCharsAfterQuote | ErrCharsAfterQuote | ErrCharsAfterQuote |
| CR 行尾待定 | ErrBareCR | ErrBareCR | ErrBareCR | r,→FStart | ErrBareCR |
EOF：FStart/Bare → 最后字段并结束（0 字节流无记录）；Quoted → ErrUnclosedQuote；QQuote → 关闭后结束；CR → ErrBareCR。
列数在第二条记录起与首条比对，不符报 ErrColumnCount；位置为该记录结束处。
