# DESIGN

## 1. 空行与单列空值
选择：每个换行提交一条记录；空行=`\n\n`产生记录 `[未引号空字段]`；末尾换行不产生额外记录；零字节=零记录。
因此单列表 `a\n\n` 解析为记录 `[a]`、`[未引号空]`。回写：记录间用 `\n` 连接并整体追加一个 `\n`，
单列空记录恰好写为两个连续换行，不加引号也不丢数据（空行天然编码该记录）。加引号仅在「原本带引号」或
「值含 `,` `"` `\r` `\n`」时进行；单列为空且未引号不属于必要情形。规范输入=解析成功且带引号字段都满足
最小引号（无冗余引号）；此时 `Write(Parse(x))==x` 逐字节成立。

## 2. par 切点
按偏移把 buf 切成 K 段。每段在两种假设下各跑一遍核心状态机：起点在引号外(out)、起点在引号内(in)，
另用 carryCR 表示段首 `\n` 需先终结挂起 CR（仅 out 可能）。每遍产出：段末状态（含 quoteSeen）、
完整记录、段首开口字段（仅 in，承接前段值）、段末开口字段、首个错误。从第 1 段(out)起，前段段末状态
唯一确定下段假设：quoteIn 用 in，否则 out（段末 CR pending 由新段首字节消化）。拼接：in 段段首开口字段
与前段段末开口字段合并。记录号/字段号 worker 内按段内 0 基计数，master 用累计记录数 rb 与开口字段基号
fb 整体加基；错误 Offset=段基址+段内偏移（绝对），Record/Field 同样 +rb/+fb 再 +1。错误只在被选为真实
假设的那遍出现，故类型/位置与单流一致。每段每字节最多算两遍，无反复重扫。

## 3. CR 待定
状态 CR：未引号字段刚消费 `\r`（未入值、行未提交）。下一字节为 `\n`→提交记录(CRLF 分隔符，值不含 CR)；
为其他→CR 入值并立即报 ErrBareCR（位置=该 CR 字节）。半包：CR 在 chunk 末尾则状态保留到下次 Feed；
Close 时仍在 CR→ErrBareCR。par：CR 在段末→段末带 carryCR，真实假设为 out；下段以 carryCR 起跑，
见 `\n` 正常提交，否则先报 ErrBareCR 再按普通字节处理。

## 4. 上限“立刻”
字段字节计数在字节真正属于字段值时自增：未引号普通字节、引号字段内普通字节与 `""` 转出的一个引号字节
（一对引号只+1）；分隔符/换行/起始引号不计数。每接收一个入值字节先判 len>=MaxFieldBytes 即报错，
故第一个超限字节到达时立即失败，不缓冲整记录。字段数：新字段诞生瞬间（起始引号或首个值字节）超
MaxFields 即错；记录数：提交瞬间超 MaxRecords 即错；已产出记录保留，错误终态，后续 Feed 返回同一错误。

## 状态转移表（状态×字节类 → 下一状态/动作；字段计数 n）
| 状态 | `,` | `"` | `\n` | `\r` | 其他 |
|---|---|---|---|---|---|
| FRESH(n) | emit空, FRESH(n+1) | QUOTE(quoteSeen) | endRec, FRESH(0) | CR | DATA, 入值 |
| DATA | emit, FRESH(n+1) | ErrQuote | endRec, FRESH(0) | CR | 入值 |
| QUOTE | 入值逗号 | Q2 | 入值\n | 入值\r | 入值 |
| Q2 | emit, FRESH(n+1) | QUOTE, 入值引号 | endRec, FRESH(0) | CR | ErrCharsAfterQuote |
| CR | endRec, FRESH(0) | 补CR入值+ErrBareCR | endRec, FRESH(0) | 补CR入值+ErrBareCR | 补CR入值+ErrBareCR |

EOF：QUOTE/Q2→ErrUnclosedQuote；CR→ErrBareCR；无任何字节→空；否则提交末记录（无尾换行合法）。
