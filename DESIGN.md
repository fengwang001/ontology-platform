# DESIGN

## 1. 空行与单列空值
记录分隔符只有 `\n`（`\r\n` 等价于一个 `\n`）。两个相邻换行之间 0 个字节，
其中的「字段」无法携带引号标记（它不是任何词法单元）。选择：**空行被跳过，
不是记录**。因此单列表中一条真正的空值记录必须与「没有记录」可区分，只能写成
`""\n`——写裸空行会被解析器跳过，数据丢失。单列空字段回写规则：`quoted` 或
（单列表且值为空）时必须写 `""`；其余仅在含 `,` `"` `\r` `\n` 时加引号。
规范输入：无空行；全部用 `\n`；裸 `\r` 只允许出现在引号内；单列空记录写作 `""`。

## 2. CR 待定
未引号态见到 `\r` 进入 `crPending`（不输出任何内容，记录起始偏移）。下一字节为
`\n`：行结束；为其他字节（含 EOF）：该 `\r` 是孤立 CR，错误位置记在该 `\r`。
半包续传时该状态可跨 `Feed` 挂起（不持有对旧切片的引用：偏移/状态已入状态机）。
引号态内 `\r` 是普通内容，原样保留；引号闭合后的 `\r` 仍走 `crPending`。

## 3. 并行切点
每段 w=0..K-1 独立建 lexer，按**入口状态假设**解析本段（段长 0 跳过）：
假设 H0「入口在引号外/字段开始」、H1「入口在引号字段内」总是各算一遍；
另惰性计算 H2「入口为 quoteSeen」、H3「入口为 crPending」，仅在驱动判定上一段以
CR 待定结束时需要（H2/H3 都是 H0/H1 跳过首字节后的结果，不产生新扫描）。
段 w 的真实入口状态由段 w-1 的终态决定：普通完成→H0；引号中→H1；
quoteSeen 且首字节是 `"`→H2；上一段以 `\r` 待定结束：本段首字节为 `\n` 用 H3 且
该 `\n` 被当行尾，否则 H3 且在 `\r` 偏移上产生「孤立 CR」错误。段首若是引号外空行
（`\n` 开头且无活跃行）记 leadingBlank 直接丢弃。首段恒为 H0。
拼接：各 collector 产出事件（字段/空行/行尾/终态错误）；跨段字段按值字节拼接、
引号标记与起始偏移取前半段；段内坐标加段起始偏移；记录号=上游已闭合记录数+段内
计数（错误发生在被拼接字段时字段号取全局活跃字段序号）。每段只扫描本段字节、
互不共享内存，无 sleep；总处理次数=H0+H1 两遍（惰性假设零额外字节），故 ≤2N+C。

## 4. 上限「立刻」
字段长度按**逻辑字符**计：普通字节 +1；引号态内 `""` 折叠为 1 个字符（转义对在第
二字节时计数 0）。长度在**每个内容字节进入时**检查，第一超限字节到达即拒绝。
字段数在字段「开始」时计数（含 EOF 隐式闭合字段），超限即错。记录数在记录闭合时
计数。任一上限错误即刻成为终态错误：sink 返回错误使 lexer 停机，之后 Feed/Close
返回同一错误实例。已完整闭合的记录保留。

## 5. 状态转移表
| 状态 \ 输入 | `,` | `"` | `\r` | `\n` | 其他 |
|---|---|---|---|---|---|
| fieldStart | 结束空字段→fieldStart | →quoted | →crPending | 空行/行尾→fieldStart | 内容→unquoted |
| unquoted | 结束字段→fieldStart | ErrQuoteInBare | →crPending | 行尾→fieldStart | 内容 |
| quoted | 内容 | →quoteSeen | 内容(保留) | 内容(保留) | 内容 |
| quoteSeen | 结束字段→fieldStart | 输出`"`→quoted | →crPending | 行尾→fieldStart | ErrCharsAfterQuote |
| crPending | 结束字段→fieldStart | （先孤立CR错） | （先孤立CR错） | 行尾→fieldStart | （先孤立CR错） |
EOF：quoted/quoteSeen→ErrUnterminated；crPending→ErrBareCR；其余 flush 最后字段。
错误四类：ErrQuoteInBare、ErrCharsAfterQuote、ErrUnterminated、ErrShape（列数不一，
table 层），另有 ErrBareCR；均为 PosError{Off,Record,Field}+哨兵，上限三类哨兵。
