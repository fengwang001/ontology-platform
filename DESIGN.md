# DESIGN

## 1. 空行与单列空值
选择：空行 = 一条含一个「未引号空字段」的记录（`a\n\nb` 是 3 行）。末尾换行不产生额外记录，因此「文件不含换行符的空输入」=0 条记录，而 `""`（无换行）=1 条记录的唯一字段为「带引号空值」，未引号的空输入无法用无引号写法表示 1 条空记录。
回写规则：记录间用 `\n`；最后一条记录的「唯一字段为空」时，无终止写法必须用 `""`（不加引号则写出空字节串，解析回 0 条记录而丢数据）；有终止写法用空行。规范输入定义：字段仅在内容含 `,` `"` `\r` `\n` 或（末记录、单列、空、无终止）时加引号；引号字段用 `""` 转义；记录间 `\n`；末记录可无终止。其余字段原样不加引号，保证 `Write(Parse(x))==x`。

## 2. par 切点
段 worker 无法判断起点是否在引号内，故每段在两种初始状态各解析一遍：H0=字段外（fresh）；H1=引号字段内（续字段）。段产出 token（完整 cell、至多一个首部续 cell、至多一个尾部未决 cell）与尾状态。拼接器从 offset 0 的 H0 开始从左到右：上段尾状态决定下段采用 H0 还是 H1；H0 段尾若为字段外未决 cell，下段 H0 首 cell 与其首尾相接合并；CR 未决跨段时上段不发终止，下一字节由拼接器判定补「行尾」或 ErrBareCR（O(1)，不重扫）。偏移均为绝对字节偏移，随 token 输出；记录号/字段号由拼接器按合并后的记录累加；错误取第一个出错段（H 确定后）的错误。双假设使每字节至多处理两遍，≤2n+O(K)。

## 3. CR 待定
状态 CR 仅存在于未引号字段末尾见到 `\r`。续传：下一字节为 `\n` → 行尾；否则 → ErrBareCR（流在 `\r` 结束时 Close 同样报 ErrBareCR）。引号内 `\r` 是普通内容，无此状态。par：切点恰在 `\r` 后时上段停在 CR 待定、不发终止；下一字节在下段首字节，由拼接器判定，故无需第三种假设。

## 4. 上限的立刻性
字段字节计数只在内容字节到达时 +1：未引号数据每字节 +1；引号内除闭合引号外每字节 +1，转义 `""` 中第二个引号作为内容 +1（按字节计数，首个超限字节即拒绝，不需缓冲整条）。字段数在每个字段开始（含首字段）时检查；总记录数在每记录首字段开始时检查。命中即终态，后续 Feed 返回同一错误。

## 5. 状态转移表
字节类：C `,`；Q `"`；N `\n`；R `\r`；D 其他。
| 状态 | D | C | N | R | Q |
|---|---|---|---|---|---|
| fStart 字段外 | 字段中,+字节 | 发空cell,新字段 | 终止记录 | CR | ErrBareQuote |
| fBare 未引号中 | +字节 | 发cell,新字段 | 终止记录 | CR | ErrBareQuote |
| fQuoted 引号中 | +字节 | +字节 | +字节 | +字节 | fQQuote |
| fQQuote 见引号 | ErrTrailingQuote | 发cell,新字段 | 终止记录 | CR待定 | 引号中,+1(转义) |
| CR 待定 | ErrBareCR | ErrBareCR | 终止记录 | ErrBareCR | ErrBareCR |
闭合引号后的 `\r` 进入 CR 待定，下字节非 `\n` 即 ErrBareCR。H1 初始=fQuoted；段末处于 fQuoted/fQQuote/CR 为未决尾不报错（末段 EOF 除外：fQuoted→ErrUnclosedQuote，CR→ErrBareCR）。
错误：ErrBareQuote、ErrTrailingQuote、ErrUnclosedQuote、ErrBareCR（lexer 语法）；ErrColumnCount（table）；ErrFieldTooLarge、ErrTooManyFields、ErrTooManyRecords（上限）。位置含字节偏移（从0）、记录号、字段号（从1）。
