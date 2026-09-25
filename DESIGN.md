# 设计推导

## 1. 空行与单列空值

两条候选：(A) 空行=一条「单未引号空字段」记录；(B) 空行跳过。

考察末尾不变量：记录必须由一个结束符（LF/CRLF）或「结束符后 EOF」终结。
若选 A，则连续两个 LF 产生空记录，而末尾最后一个 LF 也会“再产生”一条空
记录——除非再特设一个“终止符后零字节不算记录”规则；该规则又会把单列空值
（`a\n\nb\n` 中间一行）与末尾歧义混同，无法用字节形态区分，往返必丢信息。

故选 **B：空行（仅含结束符、无任何字段字节的行）跳过**。推导回写：单列
（宽度 1）表中唯一字段为空时，记录写成空行会被跳过=整条记录丢失，因此
**必须写成 `""`**；引号同时把该字段标记为 quoted。多列空字段由逗号定位，
裸写不丢。末尾 LF 不产生记录（被跳过的正是这个“空行”），无结束符的
EOF 记录合法。

## 2. par 切点

切点可能落在 5 个词法状态中的任意一个。段 worker 无法从本段字节判断入口
状态，故**每段各跑两遍**：假设 A=起点在引号外的记录边界（FStart）；
假设 B=完整续传状态（state、未闭合字段值、已解码长度、字段起止、记录/
字段序号、列宽、各计数器）由上一段被选中的 lexer 快照给出。第 0 段只有
A。每段首字节前：B 直接喂给 B 机；A 喂给 A 机。选择规则由 carry 决定：
carry=FStart 选 A；carry=Bare/Quoted/QQuote/CR 选 B——B 机的状态机本就
定义了这些状态面对首字节的动作（QQuote 遇 `"`→转义继续，遇其他→
ErrCharsAfterQuote；CR 遇 LF→结束记录，否则 ErrStrayCR），无需特判。
选完后对该机快照作为下一段 carry。两遍各吃一遍本段字节，总处理量
≤2N+O(K)，不随引号字段长度退化。所有事件（字段、记录边界、错误）都带
**全局绝对偏移**：快照里带字节计数，本段第 i 字节偏移=base+i；记录号/
字段号在快照中续编，故错误坐标与流式完全一致。

## 3. CR 待定

Bare/FStart 末遇 CR 进 CR 态但不结束记录：下一字节是 LF 才算行尾，
否则 ErrStrayCR（偏移指向该字节；EOF 则偏移=总长）。半包续传时 CR 留在
状态机内等下一次 Feed，EOF 在 CR 态即孤立 CR。par 中 CR 作为 carry 状态
传给下一段 B 机；切点恰在 CR/LF 之间时，LF 落在下段首部，B 机正常结束
记录。引号内 CR 是普通内容字节，不进 CR 态。

## 4. 上限“立刻”

字段长度按**解码后字符数**计（`""` 算 1）。机内维护 openLen：普通字节
+1；QQuote 再遇 `"`（转义成一个引号字符）+1。超限在第 MaxFieldBytes+1
个字符到达的**那个原始字节**立即报错（转义对指向第一个 `"`），不等字段
闭合，不缓冲整行。字段数在逗号开启新字段瞬间判定；记录数在第 limit 条
记录结束后，下一字节到达即 ErrTooManyRecords。错误后置终态，Feed/Close
永远返回同一错误，已产出记录保留。

## 5. 状态转移表

字节类别：`,` / `LF` / `CR` / `"` / 其他。动作用 emit(发字段)、rec(结束
记录)、app(追加解码字节)。

| 状态 | `,` | LF | CR | `"` | 其他 |
|---|---|---|---|---|---|
| FStart | FStart emit | FStart rec | CR | Quoted | Bare app |
| Bare | FStart emit | FStart rec | CR | **ErrQuoteInBare** | Bare app |
| Quoted | Quoted app | Quoted app | Quoted app | QQuote | Quoted app |
| QQuote | FStart emit | FStart rec | CR | Quoted app(1) | **ErrCharsAfterQuote** |
| CR | (续传)见下 | (续传)见下 | (续传)见下 | (续传)见下 | (续传)见下 |

CR 态仅看紧邻的下一字节：LF→FStart rec；其余→**ErrStrayCR**（EOF 同）。
EOF：Quoted/QQuote→**ErrUnclosedQuote**；CR→**ErrStrayCR**；其余：
recActive=true 则补 rec，否则无（末尾 LF 不生空记录）。
