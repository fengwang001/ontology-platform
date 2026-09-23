# 设计推导

## 1. 空行与单列空值
选择：**空行被跳过**（`""` 起记录、空串不 flush）。即连续 `\n`、首行空行、末尾换行都不产出记录。
推导：RFC4180 中「一行一个记录」，空行不含字段；若空行算记录，则末尾换行会产生幽灵空记录，与第 4 条矛盾。
后果：单列表里未引号空字段无法与空行区分（`,`、`"` 都不含）。故单列空值必须写为 `""`；
不加引号写出的空字节会被下次解析当成空行跳过，记录丢失。多列空字段无歧义（有逗号定位），写裸空。
规范输入定义：仅用 `\n` 行尾；无空行；除单列空值写 `""` 外只在含 `,` `"` `\r` `\n` 时加引号；
引号内 `""` 转义；末尾有且仅有一个 `\n`（空表为空串）。

## 2. par 切点
每段在「起点在引号外(FRESH)」与「起点在引号内(QUOT)」两种初始状态下各跑一遍状态机（双假设，每字节≤2 次）。
段内只产生：cell 事件（值/引号标记/偏移）、行尾事件、错误。前段终态决定取哪套事件：
- 前段终态 fresh/CR(空): 取 FRESH；
- 前段终态 unquoted: 若本段首字节是 `"` 则走「引号开始」分支（首 cell 由前段未完成值拼起），否则前段值与本段 FRESH 首 cell 裸拼；
- 前段终态 quoted: 取 QUOT，丢弃其幻象首 cell，并把前段已累计的开引号字段值前缀与本段首 cell 内容拼接（`""` 解码发生在各段内部，前缀串直接相连）。
每个 cell 记录全局字节偏移（段起点加段内偏移），记录号/字段号在拼接时按已完成记录数重排；错误同理：段内偏移+段起点得字节偏移，记录号加前缀记录数。两套假设在真实状态确定后输出与流式逐字节一致，因为状态机是确定性的、且拼接只丢/合跨边界的那一个不完整字段。

## 3. CR 待定
未引号态见 `\r` 进入 `crPending`：不 emit 行尾，先挂起。下一字节是 `\n` → 行尾；其它（含 EOF）→ ErrLoneCR 指向该 `\r`。
半包续传：`\r` 可独立成一个 chunk，状态挂起等下一个 `Feed`；`Close` 时仍处于该态即判孤立 CR。
par 切点：段尾 `\r` 由下一段首字节仲裁，拼接规则同 unquoted（`\n` 为行尾、否则孤立 CR 错误，偏移=该 `\r` 的全局偏移）。引号态内 `\r` 是普通内容。

## 4. 上限「立刻」
字段计数按**解码后**字节（`""` 计 1 字节，每见第二个 `"` 加 1；普通字节到达即加 1），在加入该字节前检查 `len+1 > Max`，第一个超限字节立刻返回 ErrFieldTooLarge，不缓冲整记录。
MaxFields：逗号到达、字段数已等于上限时拒绝。MaxRecords：记录完成（行尾）时计数超上限拒绝。错误存为终态错误，之后 Feed/Close 原样返回。

## 5. 状态转移表
字节类别：`,` Q(`"`) C(`\r`) N(`\n`) O(其它)。动作：f=完成字段, r=完成记录, +=追加, B=空行跳过。

| 状态 | , | Q | C | N | O |
|---|---|---|---|---|---|
| fieldStart | f/fieldStart | /quoted | /crPending | B/fieldStart | +/unquoted |
| unquoted | f/fieldStart | ErrQuote | /crPending | r/fieldStart | +/unquoted |
| quoted | +/quoted | /quoteSeen | +/quoted | +/quoted | +/quoted |
| quoteSeen | f/fieldStart | +/quoted | f→/crPending | r/fieldStart | ErrAfterQuote |
| crPending | f/fieldStart | ErrQuote | ErrLoneCR | r/fieldStart | ErrLoneCR |

Close：quoted/quoteSeen→ErrUnclosedQuote；crPending→ErrLoneCR；其余 flush 挂起字段为一条记录（空流除外）。
四类错误：ErrBareQuote、ErrAfterQuote、ErrUnclosedQuote、ErrLoneCR；列数错误 ErrFieldCount（table 层）；上限 ErrFieldTooLarge/ErrTooManyFields/ErrTooManyRecords。
