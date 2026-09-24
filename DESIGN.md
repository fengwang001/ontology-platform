# DESIGN

## 1. 空行与单列空值
流按记录切分后，**空行一律跳过**（`""` 或 `"\n"` 产出 0 条记录；`"a\n\n"` 只 1 条）。
理由：RFC4180 末尾换行不产生额外记录，且空行不含任何分隔符，无法与该规则区分。
推论：单列表里「唯一字段为空」的记录若不加引号写成空串再换行，回写结果就是空行、
会被解析器跳过，数据丢失。故 writer 对**单列记录的空字段必须写成 `""`**（加引号）。
`""` 空引号单元格 Quoted=true，与未引号空字段可区分。

## 2. par 切点
每段在两种入口假设下各跑一次词法事件流（每字节恰好一遍，双假设最多两遍）：
- H0（起点在引号外）：先看段首前一字节，若是 `\r` 则状态预置为 CR 待定（见 §3）。
- H1（起点在引号字段内）：以「引号内延续字段」开始，其首字段为续接字段，
  发出的首个 field 事件标记 merge=true（拼接到前段最后一字段，不新增字段/计数）。
从左到右确定真值：段 0 取 H0；第 i 段取前段末态——引号未闭取 H1，否则 H0。
重放事件到与串行同一个 table sink，故结果与串行逐位相同。
段内偏移+段起点偏移=全局字节偏移；段内 (recNo,fldNo)+前段末计数=全局号
（H1 merge 的首字段不增加字段号）。段末未闭引号到全局 EOF 仍未闭，报 ErrUnclosedQuote。

## 3. CR 待定与 EOF
未引号态遇 `\r` 进 CR 待定：下字节 `\n` → CRLF 行尾；其他（含 EOF）→ ErrLoneCR。
半包：`\r` 留在状态里，下次 Feed 首字节或 Close 时裁决，跨刀不丢判定。
par：切点落在 `\r`/`\n` 之间时下一段 H0 预置 CR 待定；H1 中 `\r` 永远是字段内容。
EOF 处于引号态→ErrUnclosedQuote；处于 CR 待定→ErrLoneCR（本实现选择）。

## 4. 「立刻」上限
字段字节计数：未引号逐字节 +1；引号内普通字节 +1，转义对 `""` 在第二字节（输出的那一个
`"`）+1——超限字节到达的同一步即返回 ErrFieldTooLarge，不等行尾。
字段数：每见逗号（新字段开始的一步）即 +1 判 ErrTooManyFields；
记录数：每条非空记录完成一步即 +1 判 ErrTooManyRecords。错误进入终态，Feed 原样返回。

## 5. 状态转移表
字节类：C=逗号 Q=`"` R=`\r` N=`\n` O=其他。

| 状态 | C | Q | R | N | O |
|---|---|---|---|---|---|
| fieldStart(FS) | emit空字段;IN | 开引号;IQ | 进CR | emit记录 | 开未引号字段;IU |
| inUnquoted(IU) | emit字段;FS | ErrBadQuote | 进CR | emit记录 | 追加;IU |
| inQuoted(IQ) | 追加 | 进QQ | 追加 | 追加 | 追加;IQ |
| quoteSeen(QQ) | emit字段;FS | 追加'"';IQ | ErrCharsAfterQuote | ErrCharsAfterQuote | ErrCharsAfterQuote |
| crPending(CR) | ErrLoneCR | ErrLoneCR | ErrLoneCR | emit记录(CRLF) | ErrLoneCR |

"emit记录"=发出当前字段并结束记录；空白行（0 字段）被 table 跳过。
四类语法错误：ErrBadQuote、ErrCharsAfterQuote、ErrUnclosedQuote、ErrLoneCR，
另有列数不一致 ErrColumnMismatch 与三类上限错误，全部为哨兵/具名类型，彼此可判定。

## 6. writer 最小引号
单元格 Quoted 或内容含 `,` `"` `\r` `\n` 时加引号（`"`→`""`）；
单列记录的空字段即使无引号标记也强制 `""`（§1）。记录以 `\n` 结尾，引号内 CRLF 原样。
规范输入定义：记录以 `\n` 结尾；所有非末尾记录后有换行；需要引号处均按上句加引号。
则 Write(Parse(x)) == x 逐字节；任意解析表 Parse(Write(T)) 字段值与引号标记逐位相同。
单流式解析器实例非并发安全；par 内部每段独立 lexer，K goroutine 无共享可变状态。
