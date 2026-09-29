# 设计推导（CSV RFC4180 方言）

## 1. 空行与单列空值

词法器以「字段起始」为锚点：只有见过字段（空也算字段，即遇到逗号或行尾）才产记录。
连续两个换行之间没有任何锚点 → **空行被跳过**；`\n\n` 不产记录。
多列中的空字段正常产未引号空 Cell。单列时，空值记录 `""` 与空行不可区分：
若回写空字段不写引号，记录序列化后是空串 → 再解析变成「空行被跳过」，记录丢失。
故**单列空值必须写成 `""`**；这是除含 `,` `"` `\r` `\n` 之外的第五个必要引号情形。
**规范输入**：记录间仅用 `\n`、末尾无换行、无空行、单列空值写 `""`、其余最小引号。
规范输入满足 `Write(Parse(x)) == x` 逐字节。

## 2. par 切点

每段无法判断起点是否在引号内，故每段用两个 lexer 假设各扫一遍：
H0 起点在记录边界（FIELD_START）；H1 起点在引号字段中部（QUOTED）。
从左到右，段 i 真实入口由段 i-1 的出口决定：出口为 record-boundary→选 H0；
mid-quote→选 H1；mid-unquoted→选 H0 并把首 Cell 与上段尾部拼接（引号状态在未引号段相同）；
quote-just-seen→选 H1 且把首 Cell 值前缀补一个 `"`（上一段最后字节是开引号角色）；
cr-pending→看下段首字节：`\n` 则合成行尾，否则上段末尾裸 `\r` 立即报错（与流式见到该字节同一位置）。
记录/字段号、字节偏移在拼接时由 worker 报告的**全局字节偏移**（段基址+段内偏移）
及 assembler 从 1 累计的序号得到；错误位置同理换算，与单线程逐位相同。
每段只扫两遍，总量 ≤ 2N+O(K)，不因长引号字段重扫。

## 3. CR 待定

未引号字段遇 `\r` 进 CR_PENDING 且不发射 Cell（可撤回）。
续传：下一 Feed 首字节 `\n` → 发射 Cell+行尾并清空待发；其他字节 → 裸 `\r` 错误，偏移指向该 `\r`。
`\r\n` 两字节在引号内是内容、原样保留（QUOTED 直接收下）。
par 切点落在 `\r`：上段出口 cr-pending 携带待定 Cell；按第 2 节用下段首字节裁决，
与流式在同一字节得到同一结论。流在 `\r` 处 Close：判裸 `\r` 错误。

## 4. 上限的「立刻」

字段字节数在**字符落袋时**计数：未引号每个原始字节 +1；引号字段内普通字节 +1，
`""` 对在第二字节确认转义后只 +1（第一字节时暂存、不加）。
计数在追加该字节前比较，第一个超限字节到达即返回 ErrFieldTooLarge，不等字段结束。
字段数在逗号落袋即 +1 并查 ErrTooManyFields；记录数在记录封口时查 ErrTooManyRecords。
错误后 lexer 进入终态，后续 Feed/Close 返回同一错误。

## 5. 状态转移表

字节类别：`,`  C；`"` Q；`\n` N；`\r` R；其他 X。动作：a 追加字节；c 封口字段；
r 封口记录；b 跳过空行；e 错误；d 转义追加 `"`；t 暂存待 CR 裁决。

| 状态 | C | Q | N | R | X |
|---|---|---|---|---|---|
| FIELD_START 字段起始 | c,逗号→FIELD_START | →QUOTED(开引号) | r(若有锚点)/b→FIELD_START | t→CR_PENDING | a→UNQUOTED |
| UNQUOTED 未引号中 | c→FIELD_START | e ErrQuote | a→UNQUOTED | t→CR_PENDING | a→UNQUOTED |
| QUOTED 引号中 | a | →QSEEN | a | a | a |
| QSEEN 见闭合引号 | c→FIELD_START | d→QUOTED | r→FIELD_START | e ErrBareCR | e ErrCharsAfterQuote |
| CR_PENDING | (不会见逗号；先裁决 R) | — | — | N: c,r→FIELD_START | R/X: e ErrBareCR |

错误四类：未引号中 `"`=ErrQuote；闭合后垃圾字符=ErrCharsAfterQuote；
Close 时停在 QUOTED/QSEEN=ErrUnclosedQuote；列数不符=ErrFieldCount。
另有 ErrBareCR（孤立 `\r`）与三类上限错误，均可 `errors.Is` 判定。
错误位置为触发字节的全局偏移；记录号、字段号由 assembler 当前计数给出。
