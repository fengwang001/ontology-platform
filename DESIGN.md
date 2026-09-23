# CSV 方言解析器设计（模块 ontology，仅标准库）

## 1. 空行与单列空值

**选择：空行被跳过，不产生记录。** 推导：RFC4180 下空行无字段、无分隔符，无法承载任何字段；若把空行当作「一条只含一个未加引号空字段的记录」，则 `a,b\n\nc,d\n` 的列数会从 2 列突变到 1 列而报错，空行本无意义却污染数据，故 lexer 在「字段起点直接遇行尾」时只计空行、不发 Cell/Rec 事件。末尾换行同理：换行闭合的只是「上一条」记录，不产生新记录；文件可无尾换行结束。

后果：单列表里一条**唯一字段为空**的记录，其文本形态只能是 `""`。若写成裸 `\n`，该「空记录」会按空行规则被跳过，数据丢失。因此回写器在 `len(记录)==1 && 字段为空 && !Quoted` 时**必须**强制加引号（`""`），这是第 5 条「必要引号」在逗号/引号/CR/LF 之外的第五种情形。回写保留原 `Quoted` 标记，每条记录末尾写 `\n`（含最后一条）；规范输入定义：每条记录（含末条）以 `\n` 结束、无空行、引号字段仅在必要时出现（含上述单列空值情形）、字段值原样。

## 2. par 的切点问题

切点可能落在引号字段内、`""` 中间、`\r\n` 中间，段 worker 无法仅凭本段字节得知初始引号态。方案：

1. 按近似等长把 buf 切成 K 段，切点为任意字节偏移。
2. 段 i 以**两种假设**各跑一遍 lexer：H0=段首在引号外且处于记录/字段起点（无悬挂 CR、无延续字段）；H1=由段 i−1 真实末态 `Snapshot/Restore` 克隆而来。
3. 段 0 真实态即新 lexer 态。段 i 顺序裁决：仅当左邻真实末态恰为「记录边界、引号外、无悬挂 CR、无延续字段」时选 H0，否则选 H1。
4. H1 的 lexer 带着左邻的记录号、字段号、全局字节计数与跨边界开字段的起始偏移，故其事件偏移天然是全局偏移；H0 事件加 `baseOff`，记录号加段前已完成记录数。错误随事件同一换算（字节偏移、记录号、字段号）。
5. 跨边界开字段时上游段不发该 Cell，由最终闭合它的段发出，起始偏移记原始起点，值前缀在克隆态内继续累积。

等价性：单线程 lexer 是确定型状态机；H1 段从真实状态续跑，与喂入 `buf[0:end]` 的状态机在该段字节上完全同态；H0 仅在段首确为字段起点时被选，与真实态等价。故拼接与单线程逐事件相同。每段最多两遍，`bytesProcessed ≤ 2N+O(K)`，不随长引号字段退化。H0 与段 0 立即并行（无 sleep，channel 传递），H1 沿裁决链启动；K=1 直接单 lexer。

## 3. CR 待定状态（CR_PEND）

裸字段/闭合引号后遇 `\r` 不立即定夺，进入 CR_PEND 并记住候选偏移：下一字节是 `\n` → 行尾（引号字段内 `\r\n` 为内容，不入 CR_PEND）；其他字节 → 孤立 CR，报 ErrBareCR；EOF → 同样报 ErrBareCR。半包续传时 CR_PEND 只是持久化的普通状态，下一次 `Feed` 首字节即裁决，切在 `\r`/`\n` 之间与切任意字节同构。par 段尾停在 CR_PEND 属「非记录边界」，右邻只能选 H1，由其首字节裁决，与续传一致。

## 4. 上限的「立刻」

字段字节计数在每个**内容字节**入字段前自增并与 MaxFieldBytes 比较：裸态每普通字节 +1；引号态下 `""` 整体只 +1（第二字节在 QUOTE_QUOTE 态与首字节合并计 1），引号内 CR、LF、逗号也 +1。第 `max+1` 个内容字节到达即 ErrFieldTooLong，不缓冲整条记录。MaxFieldsPerRecord 在第 `max+1` 个 Cell 时触发；MaxRecords 在第 `max+1` 条记录闭合时触发。上限错误使 lexer 进入终态（done），此后 Feed/Close 返回同一错误。

## 5. 状态转移表

输入类别：`,` `\r` `\n` `"` 普通 O；R=记录闭合，C=发字段。

| 状态 | `,` | `\r` | `\n` | `"` | O | EOF |
|---|---|---|---|---|---|---|
| FIELD_START | C→FIELD_START | →CR_PEND | 空行,R | →Q | →B | 终（无事件）|
| BARE | C→FIELD_START | →CR_PEND | R | ErrQuoteInBare | 累积 | 发字段,R,终 |
| QUOTE | 附内容 | 附内容 | 附内容 | →QUOTE_QUOTE | 附内容 | ErrUnterminated |
| QUOTE_QUOTE | C→FIELD_START | →CR_PEND | R | 附 `"`→QUOTE | ErrCharsAfterQuote | 发字段,R,终 |
| CR_PEND | ErrBareCR | ErrBareCR | R | ErrBareCR | ErrBareCR | ErrBareCR |

错误四分类：ErrQuoteInBare、ErrCharsAfterQuote、ErrUnterminated、ErrBareCR；列数不一致由 table 报 ErrFieldCount。错误携带字节偏移（从 0 起）、记录号、字段号（从 1 起）。单流式 lexer 实例非并发安全；par 内部 K goroutine 经 channel 交接，`-race` 安全。
