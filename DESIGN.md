# DESIGN

本解析器记录 = 非空字节行（空行 `\n\n` 是「一个值为空的单字段记录」，不跳过任何记录）；
文件末尾换行不产生额外观测记录，无换行结尾也合法。

## 1 空行与单列空值

不变量：解析结果只含记录、不含「行分隔符个数」这类额外信息。空行若被跳过，则单列文件中
`a\n\n b\n` 与 `a\nb\n` 不可区分；故空行必须保留为一条记录，其字段为未引号空字段。
于是记录数 = 换行个数 +（末尾非换行则 +1）。单列空值记录若回写成空串：两条相邻空值记录
`""\n""\n` 写成 `\n\n` 后再解析仍为空行记录（字段 `Quoted=false`），引号标记丢失，违反
逐字段相等。结论：writer 遇到「整条记录只有一个字段且该字段为空」时必须写 `""`（无论该
字段原本引号标记）。这也使「规范输入」（定义：空行写为 `""`，其余字段仅在含 `,`、`"`、
`\r`、`\n` 时加引号，行尾统一 `\n`，文件以 `\n` 结束）满足 Write(Parse(x))==x。

## 2 par 切点

切点可落在引号内，worker 无法仅靠本段判定入口状态。方案：解析分两遍。(a) 探测遍：用一个
丢弃 sink 的状态机把整个 buf 顺序扫一遍（每个字节恰好一次），记录每个切点处的入口快照
（状态、待决 `\r`、已累计字段字节数、记录号、字段号、字段起点、引号标记）。探测失败则
退回单遍流式解析，结果与流式完全相同（总处理 ≤ 2n）。(b) 并行遍：K 个 worker 各自从
快照初始化机器，只喂本段字节（字节处理仅 n 次），产出段内事件。机器设计上可在任意状态
中途灌入待决字段前缀（probe 已把切点前的字段前缀解码为 `pending` 注入；该注入不重复计
数，计数器只统计喂入字节），因此不需要「双假设」——入口状态由探测遍唯一确定。事件为纯
数据、顺序汇入同一 builder：每段首个未闭合字段标记 cross，仅在该段真正结束时产出 cell 事
件；含 cross 标记的段不产出该字段的 fieldEnd/fieldStart，由相邻段在切点处合并；`\r` 与
`\n` 被切开时入口快照携带 CR 待定状态，错误字节定位在喂到 `\n` 的那一段。所有偏移为喂入
时绝对偏移（probe/worker 均以段起点为基址喂字节），记录号、字段号已在快照中预载，故无
需换算；错误天然带全局坐标。

## 3 CR 待定

状态 CR：未引号字段末见 `\r`。续传：下一字节 `\n`→行尾（emit 记录，偏移记在 `\r`）；
其他字节→ErrBareCR（偏移在 `\r`）。流在 `\r` 结束（Close）→同样 ErrBareCR（`\r` 不是
合法结尾）。par：切点恰在 `\r` 后，快照状态=CR 注入下段；切点在 `\r` 前则 `\r` 由下段
正常处理。引号字段内的 `\r` 永远是内容，无待定。

## 4 上限的「立刻」

字段字节计数在「该字节被接受为字段内容」时递增：未引号态逐字节 +1；引号态中普通字节 +1，
`""` 两个输入字节解码为一个内容字符，仅在见到第二个 `"` 时 +1（即内容字节数）。计数在
append 之前比较，第一个超限字节即返回 ErrFieldTooLarge，不缓冲；同理字段号在第 limit+1 个
逗号处、记录号在第 limit+1 条记录闭合处立即报错。终态后 Feed/Close 原样返回同一错误。

## 5 状态转移表

状态：FS 字段开始；U 未引号中；Q 引号中；QS 引号后（刚见引号）；CR 行尾待定。
输入类：`,` `"` `\n` `\r` 其他。

| 状态 | `,` | `"` | `\n` | `\r` | 其他 |
|---|---|---|---|---|---|
| FS | end/start 字段 | →Q(引号字段) | end 字段+end 记录 | end 字段→CR | →U |
| U | end/start 字段 | ErrQuoteInField | end 字段+end 记录 | →CR | append |
| Q | append | →QS | append | append | append |
| QS | end/start 字段（" 为闭合） | append 一个 `"`，→Q | end 字段+end 记录 | end 字段→CR | ErrCharsAfterQuote |
| CR | ErrBareCR | ErrBareCR | end 字段+end 记录 | ErrBareCR | ErrBareCR |

Close：FS 在 0 字段时=空流（无记录），否则补 end 字段+end 记录；U 补 end 字段+end 记录；
Q→ErrUnclosedQuote；QS 补 end 字段+end 记录；CR→ErrBareCR。字段起始偏移记录在字段开始时；
cell 偏移为内容在原文中的 [start,end)（`""` 不占内容偏移，end 为最后内容字节之后）。
四类语法错误：ErrQuoteInField、ErrCharsAfterQuote、ErrUnclosedQuote、ErrBareCR；
宽度错误 ErrRecordWidth；三类上限 ErrFieldTooLarge/ErrTooManyFields/ErrTooManyRecords。
