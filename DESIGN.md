# DESIGN：半包续传 + 并行切分 CSV（RFC4180 方言）

## 推导 1：空行与单列空值

选择：**空行跳过**（两个换行之间无任何字节、无逗号）。空行语义上没有字段，若当作一条记录，则任何多列 CSV 中 `\n\n` 都会凭空产生一列记录，破坏列数一致性。
因此：单列表里值为空的记录必须以**带引号空字段 `""`** 表达；不加引号的空行被解析器跳过。回写单列空值时若 `Quoted==false` 则该记录无法区分于空行——但解析器永远不会产出这种组合（空行被跳过），故 writer 对单列、空、未引号字段返回 `ErrUnrepresentable`；解析产物中该情形必为 `Quoted==true`，写出 `""`，往返成立。
**规范输入**定义：仅在必需时加引号（含逗号/引号/CR/LF，或单列空字段）；换行用 `\n`；末尾恰好一个换行。规范输入满足 `Write(Parse(x))==x`。

## 推导 2：par 切点

段边界若落在引号字段内部，worker 仅看本段无法判断起点状态。方案：worker i 的数据取 `data[b-1:end]`（i=0 时 `b=0`，位置 0 是虚拟“引号外”字节），分别从两种入口假设各跑一遍纯函数状态机：
冷假设 = 字段开始（引号外）；热假设 = 引号字段中（入口字节记为字段内容）。
边界 `b` 处真实状态只可能是：字段开始、未引号字段中、引号字段中、刚见引号、CR 待定。前 worker 收尾状态决定后 worker 取冷/热事件流：
冷流在偏移 0 处不产生事件（无触发字节）；暖流把偏移 0 记为一个内容字节，拼接时由“前 worker 已累积的开字段”吸收，从而对任意状态都正确（CR 待定见推导 3，刚见引号与未引号中等价于在引号外进入下一字节，取冷流）。
错误位置：段内事件偏移相对 `b-1`，换算 `global = start + local - 1`（i=0 不修正）；记录号、字段号拼接时加前序段完成记录数与开字段编号。事件下标唯一，故与流式逐位一致。

## 推导 3：CR 待定

未引号字段末尾见 `\r`：下一字节是 `\n` 则为行尾，否则孤立 CR 错误；引号字段内 CR 永远是内容，无待定。待定状态 `crPending` 只是状态机状态，半包续传天然成立：`\r` 留在状态中（字段内容此前已逐字节产出），下一次 `Feed` 首字节即裁决；流在 `\r` 结束由 `Close` 判孤立 CR。par 切点在 `\r\n` 中间：前 worker 停在 `crPending` 不发记录，后 worker 冷流首字节是 `\n`，触发记录结束，与连续执行相同。

## 推导 4：上限“立刻”

字段字节计数在字节被接受为内容的同一拍递增：未引号字段每字节 +1；引号字段内普通字节（含 CR/LF）+1，转义 `""` 在第二字节确认内容时 +1（`""` 算一个字符）。计数到 `MaxFieldBytes+1` 的那个字节即返回 `ErrFieldTooLarge`，从不缓存整条记录；字段数在每字段闭合拍检查；记录总数在每记录完成拍检查。错误后进入终态，后续 `Feed`/`Close` 返回同一错误。

## 状态转移表

字节类别：`,` `\n` `\r` `"` 普通 O。动作：F=字段结束，R=记录结束，C=追加内容，Q=追加引号（quoteSeen 的 `""` 第二拍），E=错误。

| 状态 \ 输入 | `,` | `\n` | `\r` | `"` | O |
|---|---|---|---|---|---|
| fieldStart（含记录首字段） | F, fieldStart | R(空记录则跳过), fieldStart | crPending | quoted | C, unquoted |
| unquoted | F, fieldStart | R, fieldStart | crPending | E BareQuote | C |
| quoted | C | C | C | quoteSeen | C |
| quoteSeen | F, fieldStart | R, fieldStart | crPending | C+Q, quoted | E QuoteAfterClose |
| crPending | F, fieldStart | R, fieldStart | E BareCR | E（位置为该字节） | E BareCR |

EOF：quoted → E UnterminatedQuote；crPending → E BareCR；其余收尾当前记录（无任何字段的空记录丢弃）。单流式解析器非并发安全。
