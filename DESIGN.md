# 设计推导

模块 `ontology`，子包 `cell`→`lexer`→`table`→`writer`、`par`，依赖单向。单个流式解析器实例**非并发安全**；`par` 内部并发。

## 1. 空行与单列空值

取 **CRLF 为行尾、LF 为行尾**。“两个换行紧挨”即行尾后立刻又是行尾：在状态 FIELD-START 下遇到 CR/LF，选择**跳过该空行**，不产出记录（这是 RFC4180 方言对空行的通行处理）。
于是单列表里“一条只含空字段的记录”必须以 `""` 写出：若写空串，回读时在 FIELD-START 看到的是行尾，按上规则被当成空行跳过，记录丢失。故回写器在**列数恰为 1 且该字段为空（无论原引号标记）**时必须加引号。cell 用 `Quoted` 位保留 `""` 与裸空的区别。

**规范输入**（canonical）定义：记录间用单个 `\n`；文件不以换行结尾；无空行；字段仅在以下情况加引号——含 `,`、`"`、`\r`、`\n`，或（单列且值为空）；引号内容内 `""` 转义。规范输入满足 `Write(Parse(x))==x`。

## 2. par 切点

切点可落在任意字节。每个 worker 只看本段 `buf[s:e]`（含起点的全局偏移 s），无法确定 s 处真实状态，故在**两种起始假设**下各跑一次词法：

- S：字段开始（引号外）；
- Q：引号字段内部（普通 Quoted 子态）。

若切点前一字符是 `\r`（其后字节是否 `\n` 未知）或引号（其奇偶未知），把切点**左移跨过该 `\r` 或末尾连续 `"` 段**，这些字节归右段，左段只跑到新切点结束（不强制 EOF 判定）。之后两假设的初值都唯一：Q 假设从 Quoted 开始，S 假设从 FieldStart 开始。长引号段不回扫：连续 `"` 段最长为全部落在段尾的引号，两个假设合计处理该段两遍，总计 ≤ 2N+O(K)。
真实状态由左向右串联确定：第 0 段取 S；第 i 段取哪一假设由第 i-1 段结束时“是否仍在引号字段内”决定。两种假设产出的事件在“字段已打开”语义下一致，仅首字段开合不同；拼接时若本段起始于一个已打开字段，丢弃本假设的首字段开事件，字段值跨段接续。错误立即终止该假设；错误事件携带的记录/字段号为**段内序号**，拼接时加前缀计数（前段完整记录数、首字段跨段时加其字段索引）换算为全局；字节偏移本就是 `s+i` 全局值。

## 3. CR 待定

状态 CR-PENDING 仅在引号外、字段内容后遇 `\r` 时进入，不产出、不定错。续传下一字节：`\n`→行尾动作（产出字段、结束记录，换行符范围覆盖 `\r\n`）；其他（含 EOF、`,`、`"`）→BareCR 错误，偏移指向该 `\r`。引号字段内 `\r` 直接作为内容；`\r\n` 两个字节原样进入字段，不改写。半包时该状态随解析器状态挂起，无需缓存额外字节（其偏移已记录）。par 段尾若停在 CR-PENDING，按第 2 条左移把 `\r` 交右段，使切点不落在待定态。

## 4. 字段上限的“立刻”

字段长度按**解码后字节数**计数：普通字节 +1；引号内 `""` 作为整体 +1（在见到第二个 `"` 的时刻计数，而非两个）。每接受一个字节后立即与 `MaxFieldBytes` 比较，超限即刻返回 ErrFieldTooLarge，不先攒整行；流式实例随即进入终态，保存终态错误，之后 Feed/Close 恒返回它。记录字段数在每产出一个字段时比较 MaxFields；记录数在每条记录闭合时比较 MaxRecords。

## 5. 状态转移表

字节类别：`,`=C，`"`=Q，`\n`=N，`\r`=R，其他=X。动作 emit=产出字段，rec=闭合记录，skip=空行跳过，err=出错。

| 状态 | C | Q | N | R | X |
|---|---|---|---|---|---|
| FieldStart | emit空字段，→FieldStart | 开引号字段，→Quoted | skip空行，→FieldStart | →CR-Pending(空行情形) | 起裸字段，→Unquoted |
| Unquoted | emit，→FieldStart | err QuoteInUnquoted | emit+rec，→FieldStart | →CR-Pending | 累积，→Unquoted |
| Quoted | 内容 | →QSeen | 内容 | 内容 | 内容 |
| QSeen | 闭合后emit，→FieldStart | 内容'"'，→Quoted | 闭合后emit+rec，→FieldStart | 闭合后→CR-Pending(已闭合) | err AfterQuote |
| CR-Pending | err BareCR（R偏移） | err BareCR | 行尾：emit(若有)+rec/skip，→FieldStart | 不可能（待定只存一个R） | err BareCR |

EOF：Quoted→ErrUnclosedQuote；CR-Pending→BareCR；QSeen 视为正常闭合；FieldStart 且有待定首字段时不产出（末尾换行后的情形）；否则 emit 末字段并 rec（无换行记录合法）。错误四类：QuoteInUnquoted、AfterQuote、UnclosedQuote、BareCR，外加列数不一致 ErrFieldCount，均为可判定哨兵/具名类型，带字节偏移、记录号、字段号（从 1/1/0 起）。
