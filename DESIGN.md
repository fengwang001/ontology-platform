# CSV 方言解析器设计

## 1. 关键决定
- 状态机六态：F=字段开始，U=未引号字段中，Q=引号字段中，A=引号内刚见引号，X=闭合后有非法字节，C=行尾 CR 待定。输入五类：`, " CR LF other`。
- 空行 = 一条含一个**未引号空字段**的记录（见 §3.1）。
- 末尾换行不产生额外记录；文件可无换行结束；空缓冲零记录。
- 错误四类 + 三类上限，全部具名哨兵错误，位置统一 Offset/Record/Field（从 1 起，偏移从 0 起；指向第一个能判定字节：闭合后非法字节指向该字节，孤立 CR 指向该 CR，列数/字段数超限指向分隔符或换行符）。

## 2. 状态转移表（动作：B 开始字段，A 追加，E 结束）
| 状态 | `,` | `"` | CR | LF | other |
|---|---|---|---|---|---|
| F | E字段→F | B引号→Q | A(CR)→C | E记录→F | A→U |
| U | E字段→F | ErrQuote | A(CR)→C | E记录→F | A→U |
| Q | A | →A | A | A | A |
| A | E字段→F | 追加引号→Q | →C(同U尾CR) | E记录→F | ErrAfterQuote→X |
| X | E字段→F | ErrAfterQuote | ErrAfterQuote | E记录→F | ErrAfterQuote |
| C | E字段(不含CR)→F | ErrBareCR | ErrBareCR | E记录(CRLF)→F | ErrBareCR |
EOF：F/U 若本记录有内容（出现过任意字节或已开始首字段）则落记录；C→ErrBareCR；Q→ErrUnclosedQuote；A 视为闭合；X→ErrAfterQuote。引号内 `"` 不追加（A 态再见 `"` 才追加一个），故 `""` 每两个输入字节只让值增长 1。

## 3. 推导
### 3.1 空行与单列空值
空行若被「跳过」，单列 CSV 中合法的空值记录无法与纯分隔符区分，破坏「换行分隔记录」模型。故选：**空行 = 一条单字段记录，字段为未引号空值**；空文件零记录；`"a\n\n"` = 记录[`a`]、[``]。往返关键：带引号标记的空字段 `""` 与未引号空字段在「值」上不可区分，只靠 Quoted 标记区分；故 writer 规则为**原值 Quoted 的字段必须写引号**（quoted 空值→`""`），未引号空字段写空。规范输入定义：仅在必要时加引号（含 `, " CR LF` 或原标记 Quoted），记录间 LF，文件末尾恰好一个 LF；空文件为空串。
### 3.2 par 切点
每段无法自证段首是否在引号内。方案：每段用同一台机器跑**两个假设**——H0=段首在引号外（F，无悬挂），H1=段首在引号内（Q）。引理：段末引号开闭性只由段内 `"` 的配对数与段首状态决定，H1 恰为 H0 的引号翻转，因此两遍覆盖全部可能，每字节至多两遍（≤2N+常数，不因切点落长引号字段而重扫）。拼接从左到右：段 0 真值为 H0；由前一段段末状态决定本段选哪个假设。各段 cell 事件用段内偏移表达，全局坐标 = 段基址+局部偏移；段首若处 U/C/A 悬挂态，拼接层把本段首字段并入悬挂字段并按 §3.3/转移表修正首字节归属（C 首字节 LF→正常换行；A 首字节 `"`→转义引号；其余按表报错）。记录号、字段号由拼接层连续计数，错误只需偏移加段基址。
### 3.3 CR 待定
C 态暂存 CR 但不提交进值。续传来 LF：CR、LF 均不入值，正常结束记录；来其它字节（含 EOF、又一个 CR）：ErrBareCR，位置指向第一个 CR。par 切点在 CR/LF 之间：上段段末为 C，下段首字节 LF 走 C 行正常换行，否则 BareCR，偏移回填为段基址-1（指回 CR）。
### 3.4 上限「立刻」
每次追加**之前**检查 len+追加量 > MaxFieldBytes：普通字节追加 1；`""` 在 A 态见 `"` 只追加 1 个引号字节（每两个输入字节计 1），恰在第一个超限字节（含转义对第二个引号）到达时 ErrFieldTooLarge，不缓冲。MaxFields 在分隔符处新字段号算出时检查；MaxRecords 在记录结束时检查。出错即终态 terminErr，后续 Feed 原样返回。

## 4. 包与接口
- cell：`Cell{Value []byte; Quoted bool; Start,End int}`，End 为值后第一字节偏移。
- lexer：`Sink` 接口（Field(start int, q bool, v []byte)；Record(off int)；Err(*ParseError) bool）；`Limits{MaxFieldBytes,MaxFields int}`；`New(Limits,Sink)*L`、Feed/Close、`BytesSeen() int64`（非导出 bytesSeen 只读视图）。
- table：`Limits` 嵌入 lexer 两个上限并加 MaxRecords；New/Feed/Close、`Records() [][]cell.Cell`；首条定列数，不一致 ErrRecordWidth。
- writer：WriteField/WriteRecord/Write；只在必要时加引号。
- par：`Parse(buf []byte, K int, lim table.Limits) ([][]cell.Cell, error)`；每段两假设并发；非导出 bytesSeen 经包内测试钩子读取。
- 单流解析器实例非并发安全（文档明示）；par 的 K 个 goroutine 仅写各自段结果，汇合后拼接，-race 干净。
