# 设计推导

## 1. 空行与单列空值
字节层面「空行」无法与「一条只有一个未引号空字段、且无行尾结束的记录」区分：
`a\n\n` 里第 2 个 `\n` 前必须有一条记录；而裸空串没有任何记录。
结论：**每个换行恰好终结一条记录**；两个相邻换行之间是「一条含 1 个未引号空字段的记录」；
末尾换行不产生额外记录（见状态表 EOL 动作）；空输入无记录。
单列表里唯一字段为空时，若只写字段本身，`Parse("")` 得 0 条记录、数据丢失，故 writer
必须写出行尾。选择：单列空记录（quoted 或 unquoted）输出空字段后补 `\n`；
多列记录不补；末条非空也不补。
**规范输入** = writer 输出：记录以 `\n` 分隔；空输入=空表；单列空记录带尾 `\n`；
非末记录必有 `\n`；引号仅在必要时出现（见 writer 规则）。

## 2. par 切点
切点可落在引号字段或 `""`、`\r\n` 中间，段 worker 无法仅凭本段字节知道起点是否在引号内。
故每段在两种假设下各跑一次状态机：**Outside**（起点在引号外，即流式状态）与
**Inside**（起点在引号内：`inQ`、字段已开始、值为假设前缀 `"`）。
每假设产出事件（Field/EndRecord/End）与可能的错误；状态机对每字节只有唯一转移，
所以真实起点状态一旦确定，该段事件序列即确定、与流式相同。
段 i 的真实起点 = 段 i-1 的真实终点；段 0 恒为 Outside。段间携带开放字段的
(value,start) 作为下一段字段前缀（含假设 `"`），切点在 `\r\n` 间时携带 CR 待定态，
因此跨段引号与 CRLF 不被破坏。两假设各自独立内存，goroutine 安全，无共享可变状态。
偏移**不做段坐标换算**：每个 worker 都从全局字节偏移喂数（各段 `run(off, b)`），
错误坐标天然是全局坐标；记录号/字段号在拼接时按已确定的真实前缀计数平移。
每字节至多被两个假设处理 → par 计数 ≤ 2N+O(K)。

## 3. CR 待定
未引号字段末尾见 `\r`：可能是 `\r\n` 行尾，也可能是孤立 `\r`（非法），故进入 `crWait`，
不立即动作。续传时下一字节：`\n`→行尾；其它（含 EOF）→`ErrBareCR`，错误偏移记该 `\r`。
引号字段内 `\r` 是普通内容（留在 inQ，不进 crWait）。par 切在 `\r` 与 `\n` 之间时，
crWait 作为可暂停状态原样传入下一段；流在 `\r` 处 Close 即 `ErrBareCR`。

## 4. 上限「立刻」
逻辑字符计数：引号字段内除开引号外每字节 +1，转义对 `""` 的第二个引号 +1（代表一个
逻辑引号）。字段计数在到达上限后的**下一个字段字节**立即 `ErrFieldTooLarge`（不等行尾、
不缓冲整行）。记录字段数在第 (maxFields+1) 个字段开始（产生该字段的逗号）时立即
`ErrTooManyFields`；记录数在第 (maxRecords+1) 条记录开始（其前的换行终结前条时）
立即 `ErrTooManyRecords`。已产出完整记录保留；错误后解析器终态，后续 Feed 返回同一错误。

## 状态转移表（c = 逗号, q = `"`, r = `\r`, n = `\n`, o = 其它）
| 状态 \ 输入 | c | q | r | n | o |
|---|---|---|---|---|---|
| start（字段开始/引号外） | 发空字段→start | →inQ | →crWait(空字段) | 发空字段,EOL→start | 追加→plain |
| plain（未引号字段中） | 发字段→start | **ErrQuoteInField** | →crWait | 发字段,EOL→start | 追加→plain |
| inQ（引号字段中） | 追加 | →inQQuote | 追加 | 追加 | 追加 |
| inQQuote（刚见引号） | 发字段→start | 追加引号→inQ | 追加r→inQ | 追加n→inQ | **ErrCharsAfterQuote** |
| crWait（CR 待定） | **ErrBareCR** | **ErrBareCR** | **ErrBareCR** | 发字段,EOL→start | **ErrBareCR** |

EOL 动作：发出当前字段后结束记录，重置为一条新记录（首字段 start=下一偏移）但**不发**；
故「末尾换行」后再无字节即无额外记录，相邻两个换行之间得到一条单空字段记录。
Close：inQ→ErrUnclosedQuote；crWait→ErrBareCR；否则发出当前开放记录（除非已是 EOL
后重置态，该态用 finishedLastRecord 标志区分于「流首/裸空」）。
行/字段号：记录号=已 EOL 数+1；字段号=本记录已发字段数+1（错误定位用）。

## 包与 API（依赖单向 cell←lexer←table；writer←cell；par←lexer,table）
- `cell.Cell{Value string; Quoted bool; Start,End int}`：End 为字段末字节的下一偏移；
  `""` 得 Quoted=true、Value=""，区别于未引号空字段。
- `lexer.New(limits) *Lexer`；`(l *Lexer) Feed([]byte) error`；`Close() error`；
  `OnField(func(cell.Cell))`、`OnRecord(func())`；导出 `BytesProcessed() int64`。
  内部 `run(off int,p []byte, assumeInside bool) endState` 供流式与 par 复用。
- `table.Table`（表头=首记录）、`Rows() [][]cell.Cell`、`Header()`、`Parse(io.Reader)`、
  `ParseBytes([]byte)`；列数不一致→ErrFieldCount（记录号定位）。
- `writer.Write(t) []byte`：字段含 `, " \r \n` 或「单列空记录」→加引号/补行尾（见 §1）。
- `par.Parse(buf []byte,k int) (*table.Table,error)`；K=1..8，近似等长切点，
  `par.BytesProcessed() int64`（两假设处理字节之和）。
- 错误（可判定，errors.Is）：ErrBareCR, ErrQuoteInField, ErrCharsAfterQuote,
  ErrUnclosedQuote, ErrFieldCount, ErrFieldTooLarge, ErrTooManyFields,
  ErrTooManyRecords, ErrTerminal；ParseError 带 Offset/Record/Field 与 Kind。
单个流式 Lexer 实例**不并发安全**；多个实例可并行；par 内部 K goroutine 并发。
