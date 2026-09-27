# 设计推导（CSV RFC4180 方言）

## 1. 空行与单列空值
空行（两个记录终止符之间 0 字节，含 `\n\n`、`\r\n\r\n`）**跳过**，否则 `a\n\n` 无法与"a 后一条单列空记录"区分（后者无终止符可解析），且空文件会凭空多出一条记录。最后一个终止符同样不产生记录。
单列表中唯一字段为空的记录只能编码为引号空字段 `""`：因为若写裸空值，相邻终止符之间 0 字节会按上一条规则被跳过，数据丢失。故回写器"必要引号"条件：字段 `Quoted==true`（解析保留），或需规范生成时值含 `, " \r \n`，或单列表中的空值。规范输入定义：终止符仅 `\n`、末尾恰一个 `\n`、无被跳过空行、引号最小化（满足上述条件才加）。则 `Write(Parse(x))==x` 逐字节成立。

## 2. 状态转移表
类别：`,`(C) `\n`(N) `\r`(R) `"`(Q) 其他(X)。动作：emit 字段/rec 结束/blank 跳过/报错。
| 状态 | C | N | R | Q | X |
|---|---|---|---|---|---|
| fieldStart | emit空→fieldData | blank跳过→fieldStart | →crPending | →inQuoted(起点=本字节) | →fieldData(收) |
| fieldData | emit→fieldStart | rec→fieldStart | →crPending | ErrBareQuote | 收 |
| inQuoted | 收 | 收(原样) | 收(原样) | →quoteSeen | 收 |
| quoteSeen | emit→fieldStart | rec→fieldStart | →crPending | 收一个'"'→inQuoted | ErrQuoteAfterClose |
| crPending | emit→fieldStart(空尾字段) | rec→fieldStart | ErrLonelyCR | ErrBareQuote | ErrLonelyCR |
说明：引号字段起点偏移即开引号位置；未引号字段起点为首个内容字节，纯空字段 start=end=分隔符/终止符后位置。`""` 计为 1 个值字节。

## 3. CR 待定
裸 `\r` 进入 `crPending`（已收尾当前字段但暂不发 rec）：续传下一字节为 `\n` 才确认 rec；为 C/N 则该 `\r` 是行尾、按 rec 后再处理该字节；Q/X 报 ErrLonelyCR（偏移指向 `\r`）；`Close` 时仍 pending 同样报 ErrLonelyCR（`\r` 后必须有 `\n`，无 `\n` 结尾不合法）。半包在 R 与 N 间断开时状态被保存，行为与整体输入一致。`par` 切点在 R/N 之间时，上一段以 crPending 结束，下一段首字节 N 与正常状态机完全相同，无需特判。

## 4. par 切点与坐标换算
每段 worker 无法知道起点状态，故对每段以两种假设各跑一遍：H0=起点在引号外（fieldStart），H1=起点在引号内（inQuoted，且无开引号偏移，首字节若为 `"` 按闭合引号处理，模拟它正好是转义对的第二个引号）。coordinator 从段0 的 H0 起，依据上一段结束状态选本段采用的 run：inQuoted→H1；crPending→H1 不适用（R 后必在引号外），采用 H0；其余→H0。
拼接：H1 run 的字段是上一段最后一个未闭合字段的延续——首字段与上段尾字段做字节拼接，合并 `Quoted=true`、Start 取上段起点。H0 run 中可能出现"0 字段记录"（段首即终止符）：若上一段留有未引号未闭合字段，先补发该字段再落记录（该记录的空尾字段照常）；否则按空行跳过。EOF 时 inQuoted→ErrUnterminated（偏移取开引号起点），crPending→ErrLonelyCR。
坐标：worker 输出均带段内偏移，加段基址即全局偏移；记录号由 coordinator 的 table.Builder 统一编号，故只拼字节/事件，错误的"记录号/字段号"重算而非搬运：worker 报的语法错误仅带段内偏移，Builder 在接收事件流时赋予全局记录号与字段号，天然正确。

## 5. 上限的"立刻"
字段字节计数器在 inQuoted 中：普通 X 与 `""` 转义产出的一个 `"` 各 +1；在 +1 之后立即与 MaxFieldBytes 比较，第一个超限字节到达即 ErrFieldTooLong，不缓冲整字段。fieldData 中每收一个字节同样计数比较。MaxFields 在 emit 字段时（分隔符产生新字段编号）即检查；MaxRecords 在一条非空记录完成时即检查。进入终态后 Feed/Close 幂等返回同一错误。

## 6. 复杂度与并发
lexer 非导出计数器每字节 +1（含错误字节），流式计数 == 输入长度。par：每段最多跑 2 个假设，总处理 ≤ 2N + O(K)；拼接线性。K goroutine + WaitGroup，无 sleep。单流式 Parser 非并发安全（文档声明）；多实例独立。
