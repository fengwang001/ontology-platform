# 设计推导

## 1. 空行与单列空值

选择：**空行（零字节后紧跟行尾，含文件结束即空文件）被跳过，不产生记录**。
推导：词法器在“记录开始且尚未见到任何字段内容”时收到行尾，发出的记录标记为
blank；table 层丢弃 blank 记录。于是单列里“值为空的真实记录”必须与空行可区分，
手段是回写器对**单列表中唯一字段为空**的记录（无论是否带末尾换行）一律写 `""`。
若不写引号，该记录回写为零字节：空文件解析为空表（被跳过），数据丢失；多行时
则与相邻换行合并成空行。多列记录的空字段可保持裸空（列数由逗号确定，无歧义）。

## 2. par 切点

切点可能落在引号字段内，worker 无法从本段首字节知道真实引号态。方案：每段在
两种起始假设下各跑一次词法器——H0=字段外（新字段未开始），H1=引号字段内。
从左到右拼接时，由前一段结尾真实态选择本段正确的那份结果：结尾在引号内则选 H1，
否则选 H0。H1 的“段首前导”视为半截引号字段（不发字段开始事件），与前一段
epilogue 的半截字段按字节拼接成一个字段。每份假设输出 prologue（首行尾之前的
部分）、完整记录、epilogue（末尾半截字段的状态快照，含已积累字节与引号标记）。
切点落在 `""` 中间时：H1 状态为 quoteSeen，下段首字节 `"` 闭合转义，拼接逐字节
成立。错误坐标：段内事件全部带词法器的全局起始偏移 `base`（记录/字段号为段内
局部号），table 在拼接重放时得到的字节偏移天然全局；记录号在拼接时把前面各段
已提交（非 blank）记录数累加进去；字段号只在“跨段的那一个字段”上累加前段号，
其余字段段内编号即全局编号。切点落在 `\r\n` 之间：下段在选定假设下若以 `\n`
开头且 carry 为 crPending，该 `\n` 不产生新字段，仅确认行尾（见 §3）。

## 3. CR 待定

词法器在未引号字段中收到 `\r` 进入 crPending：下一字节是 `\n` 则行尾成立；
其他任何字节（含逗号）则该 `\r` 是孤立 CR，报 ErrBareCR（位置指向 `\r`）。
半包续传时 crPending 是普通持久状态，可随
Feed 暂停。par 切点落在 `\r` 后：crPending 进入 carry，下段重放时先注入 carry
再喂本段首字节。流在 `\r` 处结束（EOF）：按方言无后续 `\n`，判 ErrBareCR
（裸空记录 `"\r"` 也报错，与裸字段一致）。引号字段内 `\r` 只是内容字节，
不进入 crPending。

## 4. 上限“立刻”

字段字节计数器在每**逻辑字符**进入字段内容时 +1：普通字节即时计 1；引号内
`""` 转义对只在见到第二个 `"`（确认转义）时计 1；第一个 `"` 暂不计数
（若随后字段异常闭合则转 ErrQuoteAfterClose，不涉及长度）。计数在字节到达的
同一处理步与 MaxFieldBytes 比较，超限立即 ErrFieldTooLong，不缓冲整条记录。
MaxFields 在每个逗号（新字段号产生）时检查；MaxRecords 在 table 每接受一条
非 blank 记录时检查。错误经终态缓存，之后 Feed/Close 返回同一错误。

## 5. 状态转移表

字节类别：C 逗号，Q `"`，N `\n`，R `\r`，O 其他。
动作：B 追加内容字节，F 结束字段，rec 结束记录，e* 见错误哨兵。

| 状态 | C | Q | N | R | O |
|---|---|---|---|---|---|
| fieldStart（未触内容） | F,字段+1→start | →inQuote | rec（blank 若未触） | →crPending | B→inBare |
| inBare | F,字段+1→start | eBareQuote | rec | →crPending | B |
| inQuote | B | →quoteSeen | B | B | B |
| quoteSeen | F,字段+1→start | B（计1）→inQuote | rec | →crPending | eQuoteAfterClose |
| crPending | eBareCR | eBareCR | rec | eBareCR | eBareCR |

EOF：inQuote→eUnclosedQuote；crPending→eBareCR；其余正常收尾（有未结束字段
则 F 并 rec；空流无记录）。quoteSeen 于 EOF 为正常闭合。
