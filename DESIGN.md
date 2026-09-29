# 推导（第三节）

## 1. 空行与单列空值
空行（两个行尾相邻，或流首即行尾）= 0 字段的记录；普通行至少 1 字段，故解析器跳过 0 字段记录。
因此"单列表里唯一字段为空"的记录是字段开始后紧接行尾，即空字节（如 `x\n\ny` 中间）或文件为 0 字节。
该记录不能写成空字节（=无记录，与空文件混淆），也不能写成空行（空行被跳过）。唯一可往返写法是
`""`：空但加引号。故回写器必要引号额外包括"单列表中的空字段"（多列空字段不必，字段数已由逗号给出）。
规范输入 canonical：引号仅当字段含 `,` `"` `\r` `\n`，或单列空字段；记录间用 `\n`；无尾换行。

## 2. par 切点
每段 [s,e) 无法凭本段判断起点是否在引号内。对每段以"起点在引号外(out)"和"起点在引号内(in)"两种
初始状态各跑一遍同一字节状态机。段 0 真实假设为 out；段 k 真实起点引号状态 = 段 k-1 真实假设跑到
段尾的引号状态（引号状态只在字段存活，行尾即归零）。由此从左到右唯一确定每段假设并拼接事件流。
双假设使每字节最多处理 2 次，与切点位置无关，不会反复重扫。段尾挂起的 `\r` 属于前段（行尾），
随引号状态一起作为边界 carry 交后段，in/out 各有一份 carry。坐标换算：全局偏移 = s + 段内偏移；
记录号 = 前段真实假设行尾累计数 + 段内行尾序（首段从 1 起）；字段号在跨段同一记录内不重置。
错误携带段内 (偏移,记录,字段)，拼接时加同样的全局基数。

## 3. CR 待定
未引号字段末尾见 `\r` 进 CRpend，不立即判定：下一字节 `\n` → 行尾；其他字节或 EOF → ErrLoneCR，
位置在该 `\r`。半包续传中 CRpend 是可持久化状态，挂起 `\r` 尚未计入值。par 中切点落在 `\r`/`\n`
之间时，`\r` 归前段并随 carry 交后段：后段首字节为 `\n` 则行尾，否则前段结论即 lone-CR 错误。
流在 `\r` 处 Close → ErrLoneCR。引号字段内 `\r` 只是内容，无待定。

## 4. 上限"立刻"
`""` 在"见引号后"态遇到第二个 `"` 时只产出 1 个 `"` 字符，故字段字节计数在"向字段值追加一个字节"
的动作处递增（转义对只 +1），追加前判断 MaxFieldBytes+1 是否超限，到达第一个超限字节即拒绝，
不缓冲整行。MaxFields 在逗号落字段时判；MaxRecords 在记录闭合时判。

## 状态转移表（状态 × 字节类 → 下一状态 / 动作 / 错误）
字节类：`,` `"` `\n` `\r` other。
- fStart(字段开始): `,`→fStart 发空字段; `"`→fQuote 开引号字段; `\n`→fStart 0字段行跳过; `\r`→CRpend; other→fField 追加
- fField(未引号中): `,`→fStart 发字段; `"`→ErrBareQuote; `\n`→fStart 发字段+行尾; `\r`→CRpend; other→fField 追加
- fQuote(引号中): `"`→fQQuote; 其余四类→fQuote 原样追加（`\r` 内容保留）
- fQQuote(刚见引号): `"`→fQuote 追加一个引号; `,`→fStart 发字段; `\n`→fStart 发字段+行尾; `\r`→CRpendQ; other→ErrQuoteGarbage
- CRpend(未引号闭合前): `\n`→fStart 发字段+行尾; 其他/EOF→ErrLoneCR
- CRpendQ(引号已闭合后): `\n`→fStart 发字段+行尾; 其他/EOF→ErrLoneCR
- EOF: fStart→正常; fField/fQQuote→发字段+行尾(无尾换行记录); fQuote→ErrUnclosedQuote; CRpend*→ErrLoneCR

fField 遇 `\r` 时字段尚未发出，CRpend 见 `\n` 时补发。`\r\n` 原样保留仅发生在 fQuote 内。
错误哨兵：ErrBareQuote、ErrQuoteGarbage、ErrUnclosedQuote、ErrLoneCR、ErrColumnMismatch、
ErrFieldTooLarge、ErrTooManyFields、ErrTooManyRecords、ErrTerminal。
单流式解析器实例非并发安全（文档明示）；par 内 K 个 goroutine 仅读共享输入、各自持有独立状态，race 安全。
