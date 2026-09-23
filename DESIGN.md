# DESIGN

## 1. 空行与单列空值（推导）
不变量 5 要求 Parse(Write(T)) 的引号标记逐字段相同。若空行被跳过，则单列输入 `a\n\n` 的记录 `{a}` 与 `a` 无法区分；更关键：单列中一条"唯一字段为空且未引号"的记录，若 writer 输出空行，该空行会按"跳过"语义丢失，破坏往返。故：**空行不跳过，它是一条恰好含 1 个未引号空字段的记录**。单列空值记录写回时 writer 必须输出 `""`（加引号），因为不加引号写出空字节在记录边界处无法与"无记录"区分。必要引号集合由此含第 5 项：**未引号空字段且该字段是单列表的唯一字段**。规范输入：每条记录以 `\n` 结尾；字段仅在含 `,` `"` `\r` `\n` 或上述"单列空值"情形时加引号（`"` 写为 `""`）。

## 2. par 切点（推导）
切点可落在任意字节，worker 无法从本段字节知道起点是否在引号内。方案：每段以两种入口假设各跑一遍——Hout=字段起点(引号外)、Hin=引号字段中。段 0 只有 Hout 为真；段 i+1 的真假设由段 i 真实出口状态决定：出口处于引号内状态则 Hin 为真，否则 Hout 为真（CR 待定为引号外；FieldStart/AfterQuote 等都归 Hout，首事件的位置/引号标记用段尾重放的 1 字节修正，见第 4 节）。因此每字节最多在两个假设中各处理一次，O(2N)，长引号字段内重复切点不会重扫更老的数据（每段独立、顺序状态仅传递 1 字节）。段内事件先记段内坐标（记录号/字段号从 1 起、字节偏移为段内偏移）；拼接时：全局字节偏移 = 段起点偏移 + 段内偏移；全局记录号 = 前段已闭合记录数 + 段内记录号（跨段记录只在其闭合处产生一条 RecordEnd，字段由两段片段合并：引号内跨界字段拼接解码后的内容并保留引号标记，未引号跨界字段同理拼接）。错误在被确定为真的那条假设的轨迹上报告，坐标按同式换算；列数错误只在拼接出的完整记录上判定，故与单线程同一位置同一记录号。

## 3. CR 待定（推导）
状态 CR：未引号字段末尾刚见 `\r`。半包：`\r` 留在状态机内不产出任何事件，下一字节是 `\n` 则行尾（RecordEnd），否则它是孤立 `\r`，立即 ErrBareCR，位置指向该 `\r`；EOF 停在 CR 同样按孤立 `\r` 报错（不把流尾当行尾）。par：切点恰在 `\r\n` 之间时，`\r` 是前段最后字节，前段真实出口=CR（引号外），下一段 Hout 路径见到 `\n`；拼接器把"段尾未决 CR"与段首 `\n` 合成行尾，位置用前段 `\r` 的全局偏移；若段首非 `\n`，在该位置合成 ErrBareCR。引号内 `\r` 不进 CR，原样入字段，`\r\n` 因此整体保留。

## 4. 上限"立刻"（推导）
字段字节计数器随到达字节递增，不先攒记录。未引号字段：每来一个内容字节先判上限再入值；引号字段：原始内容计入 `""` 两个字节，但题面要求 `""` 算一个字符，故计数按"解码后输出字节数"——每个普通引号内字节 +1，`""` 这对合计 +1；仍可在第一个超限输入字节（转义对的第二个引号）到达时判定。字段数：字段起点事件到达即计数。记录数：RecordEnd 到达即计数。超限返回具名 LimitError，已闭合记录保留，解析器进入终态，后续 Feed/Close 返回同一错误。

## 5. 状态转移表
输入类别：c=逗号 q=`"` n=`\n` r=`\r` o=其他普通字节 e=EOF。动作：emit=产出字段字节，F=字段结束，R=记录结束。

| 状态 | c | q | n | r | o | e |
|---|---|---|---|---|---|---|
| FieldStart | F→FieldStart | →Quoted | F,R→FieldStart | →CR | emit→Unquoted | F（空输入不产出） |
| Unquoted | F→FieldStart | ErrQuote | F,R→FieldStart | →CR | emit→Unquoted | F |
| Quoted | emit | →QQuote | emit | emit | emit→Quoted | ErrUnterminated |
| QQuote | F→FieldStart | emit(`"` )→Quoted | F,R→FieldStart | F→CRAfter | ErrTrailingQuote | F |
| CR | F,R→FieldStart | ErrBareCR | F,R→FieldStart | ErrBareCR | ErrBareCR | ErrBareCR |
| CRAfter(闭合后) | F→FieldStart | ErrTrailingQuote | F,R→FieldStart | →CR | ErrTrailingQuote | F |

错误：ErrQuoteInUnquoted、ErrTrailingQuote、ErrUnterminated、ErrBareCR、ErrColumnCount；均带字节偏移/记录号/字段号。
