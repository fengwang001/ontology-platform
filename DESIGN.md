# DESIGN

## 1. 空行与单列空值

定义：记录边界为裸 `\n`/`\r\n`，或 EOF 处的悬挂字段。
连续两个行边界之间没有任何字节（含 `\r\n\n`）→ **空行跳过**，不产出记录。
推导：若空行产出 `[""]`，则输入 `a\n\n` 无法与单列表里真实的「值为空」记录区分；
单列表中 `a,` 与 `,a` 都已说明有两个字段，只有空行这一种歧义源，故消除它（跳过）。
边界细节：流首空行同样跳过；末尾换行不产生额外记录；`""` 不是空行（有两个字节，
产出一个加引号空字段）。单列表的空值记录必须写成 `""` + 行结束：
`Parse("\"\"\n")` → `[quoted empty]`；若写裸空 `\n`，解析端按空行跳过，数据丢失。

## 2. par 切点方案

切点 c 可为任意偏移。若 c == len 或 buf[c]=='\n' 或 buf[c-1]=='\r'，
则边界天然落在引号外，直接切；否则把 c **向右推到其后第一个引号外的 `\n` 之后**
（末尾段推到 EOF）。推进只跳过属于同一物理行的字节。每段以两种起始假设各解析一遍：
假设 OUT（起点在引号外，从状态 fieldStart 开始）与假设 IN（起点在引号内，
从 qField 开始且携带一个起始于段首的悬挂字段）。段尾实际落在引号内 ⇔
「到段尾为止的 `"` 个数奇偶 + OUT/IN 初态」判定，与逐字节状态机一致。
从左到右：第 0 段选 OUT；其后每段，前段真实结束在引号内则本段选 IN，否则 OUT。
选 IN 时本段首个 cell 是跨界字段的后半：与前段最后一个 cell 按偏移区间拼接
（value 直接相连、引号标记保留、Start 取前段、End 取本段）。跳过的行不在任何段边界上，
因此事件流拼接 = 单线程事件流；错误随选中假设产生，其段内偏移加段起点即为全局字节偏移；
IN 假设在段内闭合引号时记录闭合处相对段首的字段序号差，字段号 = 前段字段号 + 该差值；
记录号 = 前段结束记录号 + 段内行结束计数。OUT 假设从 1 计，再加前段累计。
复杂度：每段最多两遍，总处理 ≤ 2N + O(K)，长引号字段不引发重扫。

## 3. CR 待定状态

状态 `crPending`：裸字段中见到 `\r`，不 emit、不计入 value，等下一字节。
下一字节为 `\n` → 行结束（`\r\n` 整体为终止符，`\r` 不入值）；否则 → ErrLoneCR，
错误偏移指向该 `\r`。半包续传：`\r` 留在状态里跨 Feed，`Close` 时仍处 crPending
则判 ErrLoneCR（孤立 `\r` 流尾非法）。引号内的 `\r` 是普通内容，原样保留；
引号内 `\r` 后非 `\n` 也合法。par：切点不允许落在 `\r` 与 `\n` 之间（见 §2 天然边界规则），
若落在 `\r` 上则随其后第一个引号外 `\n` 整体归入左段，故 worker 内 CR 永不跨段悬挂。

## 4. 上限的「立刻」

字段字节计数 = 解码后的内容字节：裸字段每吃一个内容字节 +1；引号字段内每吃一个内容
字节 +1，转义对 `""` 在见到第二个 `"` 时 +1（只产出一个引号字符）。每 +1 后立即与
MaxFieldBytes 比较，第一个超限字节到达即 ErrFieldTooLarge，不缓冲整条记录；
par 中跨界字段在拼接时计数，超限点的全局偏移 = 前段起点 + 段内累计位置。
MaxFields：每 emit 一个 cell 前计数 +1 并立即判定。MaxRecords：每闭合一条非空记录时
判定。终态：记录错误后 Parser 冻结，后续 Feed/Close 返回同一错误（已产出记录保留）。

## 5. 状态转移表

状态：S=fieldStart，B=bareField，Q=qField，D=qQuote（引号内刚见引号），C=crPending。
输入类别：`,` `\n` `\r` `"` 其他字节 x；EOF。

| 状态 | , | \n | \r | " | x | EOF |
|---|---|---|---|---|---|---|
| S | emit空→S | 空行→S | C | ErrBareQuote | B(存x) | 无事件结束 |
| B | emit→S | emit+行结束→S | C | ErrBareQuote | B(存x) | emit悬挂→结束 |
| Q | 存, | 存\n | 存\r | D | 存x | ErrUnclosedQuote |
| D | emit→S | emit+行结束→S | C(emit后) | 存"→Q | ErrCharsAfterQuote | emit悬挂→结束 |
| C | ErrLoneCR | 行结束→S | ErrLoneCR | ErrLoneCR | ErrLoneCR | ErrLoneCR |

emit 偏移：S 起点 [pos,pos)；B [start,pos)；Q/D [start,end)（去外层引号与转义）。
错误类型：ErrBareQuote、ErrCharsAfterQuote、ErrUnclosedQuote、ErrLoneCR；
列数不一致 ErrColumnCount；上限 ErrFieldTooLarge/ErrTooManyFields/ErrTooManyRecords。
