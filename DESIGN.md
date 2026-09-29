# CSV 解析器设计（RFC 4180 方言）

分隔符 `,`；记录结束 `\n` 或 `\r\n`；引号 `"`；引号内 `""` 表示一个 `"`，逗号/CR/LF/CRLF 均为内容且 CRLF 原样保留。

## 1. 空行与单列空值

选择：**空行（本记录开始后未消费任何字节即遇行尾）被跳过，不产生记录**。若空行算作「一个未引号空字段的记录」，则单列输入 `a\n\n` 无法区分「空行分隔符」与「值为空的记录」。跳过规则下，单列空值记录必须用 `""` 表达：`a\n""\n`。

回写器因此在**单列表中唯一字段为空（无论原引号标记）时必须加引号**，写 `""`；不加引号会写成空行，再次解析被跳过，字段丢失。其他列的未引号空字段写空串即可（逗号已保证字段存在）。

规范输入定义：记录间恰好一个行尾、统一使用 `\n`、末尾有 `\n`、无被跳过的空行、引号仅在必要时出现（含 `,` `"` `\r` `\n`，或单列空字段）。对规范输入 `Write(Parse(x)) == x` 逐字节成立。

## 2. par 切点

切点可落在任意字节。每段 worker 无法独立得知入口是否在引号内，因此**各跑两条假设**：入口在引号外（seed=fieldStart）与入口在引号内（seed=inQuoted）。引号外假设还需覆盖「上一段恰好停在 `\r`」——令该假设入口 seed=crPending：此时段首若为 `\n` 即正常行尾，否则（含段空 EOF）按孤立 CR 报错，与流式左到右的判定一致。

段 i 的真实入口状态由段 i-1 的真实出口状态决定（段 0 恒为引号外）：出口为 inQuoted 选 inQuoted 结果；出口为 crPending 选 crPending 结果；其余选 fieldStart 结果。被选中的假设自段首起字节分类与真实流式执行完全相同（确定性状态机），故段内 token 与错误完全相同，无需重扫，每字节最多 2 次。

跨段的同一字段由相邻段拼接：引号外出口时若字段未闭合（bare 中/crPending/fieldStart 且本段已有字段数据），与下段首字段合并；引号内出口必合并；afterQuote 出口字段已闭合不合并。合并段 `Start` 取首段起点、值拼接、引号标记 OR。记录号/字段号由 table 组装器在拼接流上重新计数；字节偏移在 worker 内用段基址直接算全局值（seed 携带 base，token 偏移=base+段内位置），错误偏移同理。

## 3. CR 待定

bare 中或闭合引号后见到 `\r` 进入 pending（C/D）且不把 CR 计入值：下一字节为 `\n` → 字段结束+记录结束（值不含 CR）；非 `\n`（含 EOF）→ 孤立 CR，报 ErrBareCR（偏移指向 CR）并进入终态。半包续传时 pending 是持久状态，挂起在 seed 中，跨 Feed 判定与同一缓冲内完全一致。流在 `\r` 处 Close：无后继字节，按孤立 CR 报错。引号内 `\r` 永远是内容字节，不进 pending。par 中 pending 落在切点：上段出口 pending，下段 worker 的 pending 假设首字节见 `\n` 成行尾、见其他（或段末仍空收尾）报 ErrBareCR；C/D 在切点后行为相同（字段均已闭合），故只需一条 pending 假设。

## 4. 上限的「立刻」

字段逻辑字节数 = 值的字节数，引号内 `""` 两个源字节只追加 1 个字节（在追加那一个字节时计数 +1）；bare 中每追加 1 字节 +1。每追加前检查 `curLen+1 > MaxFieldBytes`，超限立刻返回 ErrFieldTooLong，不继续缓冲。字段数在字段开始（首个字段随记录开始计数、遇逗号时对下一字段计数，即使逗号在最后）时检查 `> MaxFieldsPerRecord`。记录数在一条记录完成且被接受（非空行）时检查 `> MaxRecords`。任一错误后解析器进入终态，记住首个错误；后续 Feed/Close 返回同一错误。上限 0 表示不限。

## 5. 状态转移表

状态：S=fieldStart，B=bareField，Q=inQuoted，A=afterQuote，C=bareCRPending，D=quoteCRPending。输入类：`,` `"` `\r` `\n` 普通字节 O。动作：emit=结束当前字段（空字段在 S 遇 , 或行尾时也 emit），eol=记录结束（若本记录尚无任何字段则为空行，跳过且不 emit），add=追加值字节，q=追加一个引号字节。**任何语法错误都使解析器进入终态**，sticky 返回同一错误（与上限一致），因此错误转移不再列出后继。

| 状态 | `,` | `"` | `\r` | `\n` | O |
|---|---|---|---|---|---|
| S | emit→S | →Q（引号起点） | →C | emit,eol→S | add→B |
| B | emit→S | ErrQuoteInBare | →C | emit,eol→S | add→B |
| Q | add | q→A（再遇 `"` 为转义） | add | add | add |
| A | emit→S | add→Q（`""` 之后续引号内） | →D | emit,eol→S | ErrGarbageAfterQuote |
| C | ErrBareCR | ErrBareCR | ErrBareCR | emit,eol→S（CR+LF 行尾） | ErrBareCR |
| D | ErrBareCR | ErrBareCR | ErrBareCR | emit,eol→S | ErrBareCR |

EOF 时：Q → ErrUnterminatedQuote（偏移=开引号）；C/D → ErrBareCR（偏移=该 CR）；S/B/A 若本记录已有字段（含已 emit 或当前字段虽空但记录已开始，如 `a,`）则隐式 emit+eol，接受最后无换行记录；S 且本记录无任何字段不产生记录（纯尾随空行）。par 的 crPending seed 额外携带该 CR 的全局偏移，供下段首字节非 `\n` 时报错定位。
