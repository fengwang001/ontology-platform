# DESIGN

## 状态机（lexer）
状态：FieldStart / Unquoted / Quoted / QuoteSeen / CRPending。逐字节处理，不回扫。

| 状态 | 字节 | 动作 | 下一状态 |
|---|---|---|---|
| FieldStart | `"` | 字段开始(Quoted=true) | Quoted |
| FieldStart | `,` | 产出空未引号字段 | FieldStart |
| FieldStart | `\n` | 若本记录已有字段则产出空字段并 EndRecord，否则跳过空行 | FieldStart |
| FieldStart | `\r` | 记 pending | CRPending |
| FieldStart | 其他 | 字段开始(Quoted=false)，字节入值 | Unquoted |
| Unquoted | `,` | 产出字段 | FieldStart |
| Unquoted | `\n` | 产出字段并 EndRecord | FieldStart |
| Unquoted | `\r` | 记 pending | CRPending |
| Unquoted | `"` | 错误 ErrBareQuote | 终态 |
| Unquoted | 其他 | 字节入值（先查字段上限） | Unquoted |
| Quoted | `"` | 待定 | QuoteSeen |
| Quoted | 其他(含`\r`/`\n`/`,`) | 字节入值（先查字段上限） | Quoted |
| QuoteSeen | `"` | 一个 `"` 入值（先查上限） | Quoted |
| QuoteSeen | `,` | 产出字段 | FieldStart |
| QuoteSeen | `\n` | 产出字段并 EndRecord | FieldStart |
| QuoteSeen | `\r` | 记 pending | CRPending |
| QuoteSeen | 其他 | 错误 ErrJunkAfterQuote | 终态 |
| CRPending | `\n` | 产出字段并 EndRecord | FieldStart |
| CRPending | 其他 | 错误 ErrBareCR | 终态 |

Close：CRPending→产出字段并 EndRecord；Quoted/QuoteSeen→ErrUnclosedQuote；Unquoted→产出字段并 EndRecord；FieldStart→若本记录已有字段则产出空字段并 EndRecord。出错后进入终态，后续 Feed/Close 返回同一错误。

## 推导一：空行与单列空值
空行（FieldStart 态遇 `\n` 且本记录尚无字段）**跳过**，不产出记录。若把空行当作「单字段空记录」，则 `a\n\nb` 与 `a\n""\nb` 解析结果相同，回写无法区分，违反往返。代价：空行在往返中丢失，因此「规范输入」定义为不含空行。推论：解析器产出的表里，单列空值记录的唯一字段必然 Quoted=true（来源 `""`），故 writer 对「单列且值为空」的字段必须加引号写成 `""`——不加引号会写成空行，再解析时被跳过，记录丢失。

## 推导二：par 切点
worker 无法仅凭本段字节判断起点状态（FieldStart/Unquoted/Quoted/QuoteSeen/CRPending 都可能）。方案：每段并行算两个假设变体——A：起点在 FieldStart；B：起点在 Quoted（引号字段中间，字段从段外延续，不产生 FieldStart 事件）。每段字节至多处理 2 次，总计 ≤2N。拼接从左到右，用「前一段所选变体的结束状态」决定本段用哪个变体及修正动作（修正都是 O(1)，不重扫字节）：
- 前段结束 FieldStart → 用 A，原样回放。
- 前段结束 Quoted → 用 B（正好从 Quoted 续上）。
- 前段结束 Unquoted（字段延续进本段）→ 用 A 但修正：段首是 `"` → 真解析应报 ErrBareQuote（合成）；段首是 `\n` → 合成 FieldEnd+RecordEnd（A 把该 `\n` 当空行跳过，事件恰好缺失）；否则丢弃 A 的首个 FieldStart 事件（A 误以为新字段，真解析是延续旧字段，后续事件逐位相同——两种状态对非 `"` 字节行为一致，遇 `,`/`\n`/`\r` 都收尾当前字段）。
- 前段结束 QuoteSeen → 用 A 但修正：段首 `"` → 合成 Data('"')（`""` 被切开）并丢弃 A 的首个 FieldStart；段首 `,`/`\r` → 丢弃 A 的首个 FieldStart；段首 `\n` → 合成 FieldEnd+RecordEnd；其余 → 合成 ErrJunkAfterQuote。
- 前段结束 CRPending → 段首 `\n` → 合成 FieldEnd+RecordEnd（若前段 pendingEmpty 再补 FieldStart）；否则合成 ErrBareCR。A 把该 `\n` 当空行跳过，事件恰好缺失。
归纳不变量：回放前 i 段后，Builder 的事件流与单线程前 boundary_i 字节产生的事件流逐位相同；边界修正只查 buf[boundary] 一个字节。坐标换算：变体创建时令 offset=段起点，故事件偏移即全局偏移；变体错误为段内 1 起编号，全局记录号 = Builder 已完成记录数 + 段内记录号，全局字段号 = Builder 当前记录已完成字段数 + 段内字段号（跨段字段恰是段内 1 号字段）。上限由 Builder 在回放 Data/FieldEnd/RecordEnd 事件时按全局计数即时判定（跨段字段的字节数自然累计），变体内部上限只会更晚触发，不影响位置正确性。末段回放完后按所选变体结束状态合成 Close 语义（Quoted→ErrUnclosedQuote；CRPending→按行尾；Unquoted/QuoteSeen→FieldEnd+RecordEnd；FieldStart 且当前记录有字段→补空字段+RecordEnd）。

## 推导三：CR 待定
未引号字段（或引号字段闭合后）遇 `\r` 进入 CRPending：下一字节是 `\n` 则为行尾，否则报 ErrBareCR。半包续传：`\r` 是 Feed 末尾字节时状态保留，下次 Feed 续判。par 切点：`\r` 落在段尾时该段以 CRPending 结束，拼接时看下一段首字节是否为 `\n` 决定行尾或 ErrBareCR；`\n` 落在段首时由前段的 CRPending 消费，本段从 FieldStart 重新起（A 假设正确处理：段首 `\n` 在 FieldStart 态为空行跳过，而拼接器在发现前段 CRPending 时把该 `\n` 标记为已消费）。流在 `\r` 处结束：Close 时按「`\r\n` 被截断为 `\r`」处理——视为合法行尾（产出字段并 EndRecord），因为流末尾无法再等 `\n`，且截断遍历要求「已产出记录是完整结果的前缀」。

## 推导四：上限「立刻」
字段字节计数在**内容字节入值前**检查：未引号字段每字节计 1；引号字段内普通字节计 1；`""` 在 QuoteSeen 态见到第二个 `"` 时计 1（一个引号字符）。因此第 maxFieldBytes+1 个内容字节到达时立即返回 ErrTooManyFieldBytes，不缓冲整条记录。字段数在字段产出时检查（第 maxFieldsPerRecord+1 个字段产出前报错）；记录数在 EndRecord 时检查。超限后解析器进入终态，已产出的完整记录保留，后续 Feed 返回同一错误。

## 规范输入（canonical）
满足以下全部条件的输入 x 保证 `Write(Parse(x)) == x` 逐字节成立：记录以 `\n` 结尾（含最后一条）；无空行；未引号字段不含 `"`、`,`、`\r`、`\n`；引号字段仅在必要时使用（含 `,"` `\r` `\n` 之一，或为单列空值 `""`）；引号字段内 `""` 表示引号、`\r` 只以 `\r\n` 形式出现在引号字段内；各记录列数一致。

## 并发说明
单个流式解析器实例不是并发安全的；多实例解析不同输入互不共享状态。par 内各 worker 只写自己的段结果，主 goroutine 拼接。
