# DESIGN

## 推导
1. **空行与单列空值**：空行解析为“一条仅含一个未引号空字段”的记录；跳过会丢失记录，也无法为后续行编号。`""` 与空字段靠 `Quoted` 区分。回写普通多列表时，单列空记录可写成 `\n`；但在“整张表只有该一条记录”时，单独 `\n` 表示末尾换行后的零记录，所以必须写 `""\n`。
2. **并行切点**：每段分别假设起点在引号外、引号内各跑一次。段内产生“开放前缀字段、完整事件、结束状态”。从左段的真实结束状态选择右段假设：引号内选 inside 结果，否则选 outside；开放字段与右段首个开放字段按偏移拼接，EOR/字段仅在状态真实经过分隔结构时提交。偏移直接加段起点；记录/字段号只数真实提交事件，因此与串行一致。
3. **CR 待定**：引号内 CR 是普通内容；引号外见到 CR 不立即结束记录，只保留“CR 待定”。下一字节是 LF 才在行尾提交，否则在 CR 偏移报孤立 CR。半包在 CR 处暂停；并行时该状态随段尾交给下一段。流在 CR 处结束则等同孤立 CR。
4. **上限立即性**：字段计数在每个“内容字节”进入字段时递增；引号字段中的转义对 `""` 的第二个引号产生 1 个内容字节，首引号、结束引号不计数。到达 `MaxFieldBytes+1` 的那个字节立即报错；字段数在逗号决定尚有后继字段时检查；记录数在记录完整提交时检查。

## 状态转移表
| 状态 | 字节类别 | 下一状态 | 动作 / 错误 |
|---|---|---|---|
| FStart | `,` | FStart | 提交当前字段，开始新字段 |
| FStart | `"` | Quoted | 标记引号字段 |
| FStart | `\r` | CROrEOF | CR 待定 |
| FStart | `\n` | FStart | 提交字段与记录，另起记录 |
| FStart | 其他 | Bare | 追加内容并检查字段上限 |
| Bare | `,` | FStart | 提交当前字段，开始新字段 |
| Bare | `"` | Bare | ErrBareQuote |
| Bare | `\r` | CROrEOF | CR 待定 |
| Bare | `\n` | FStart | 提交字段与记录 |
| Bare | 其他 | Bare | 追加内容并检查字段上限 |
| Quoted | `"` | QuoteSeen | 可能是转义或结束引号 |
| Quoted | 其他（含 CR/LF/逗号） | Quoted | 追加内容并检查字段上限 |
| QuoteSeen | `"` | Quoted | 追加一个引号内容并检查上限 |
| QuoteSeen | `,` | FStart | 提交当前字段，开始新字段 |
| QuoteSeen | `\r` | CROrEOF | 引号已闭合，CR 待定 |
| QuoteSeen | `\n` | FStart | 提交字段与记录 |
| QuoteSeen | 其他 | QuoteSeen | ErrTextAfterQuote |
| CROrEOF | `\n` | FStart | 提交字段与记录 |
| CROrEOF | 其他 | CROrEOF | ErrBareCR（位置为待定 CR） |
| Close | Quoted | — | ErrUnclosedQuote |
| Close | CROrEOF | — | ErrBareCR（位置为待定 CR） |
| Close | 其他（无残留开放记录） | — | 成功；不把末尾换行变成额外记录 |
