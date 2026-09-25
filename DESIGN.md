# DESIGN

## 1. 空行与单列空值
记录由终止符界定，因此空行（`\n\n`、首尾 `\n`、`a\n\nb\n` 中间）就是一条含一个**未引号空字段**的记录；
文件末尾终止符不产生额外记录。后果：`x` 与 `x\n` 都是 1 条记录，无法区分；末尾的「单列空记录」若写成裸空（`a\n\n`）会被
当尾随换行吞掉，故回写器对**单列表的最后一条空（裸）记录**必须写 `""\n`。这就是「最小必要引号」的第五种情形
（另四种：值含 `,`、`"`、`\r`、`\n`；`"` 写成 `""`）。

## 2. par 切点
每段独立词法，做**双假设**：分别假设段起点 outside / quoted，产出两条事件流及终态；另设 crPending：若上段终态为 fieldCR，
下段首字节是 `\n` 则二者合成 field+recordEnd，否则在 `\r` 偏移报 ErrBareCR。从段 0 起用「outside + 无挂起」选假设，
段 i 的真实终态决定段 i+1 选哪条流。论证：假设状态只有 outside/quoted 二值（fieldCR 单独处理），选中的流与单流状态机
对同一段字节的逐字节迁移完全一致，故拼接后事件逐位相同。记录号/字段号按拼接事件重新编号；事件携带**全局字节偏移**
（段内偏移+段起点），错误偏移天然全局；段内 quoted 假设里的未闭合错误在选中该流时才采纳。每段至多两遍，无回扫。

## 3. CR 待定
outside 见 `\r` 进入 fieldCR：不下发字段、不判错。下一字节 `\n` → 发 field 与 recordEnd；其它 → 在该 `\r` 偏移报
ErrBareCR。流式：`\r` 恰为 Feed 末字节则状态挂起，下次 Feed 续判；Close 时仍 fieldCR → ErrBareCR。par：见第 2 条
跨段处理；段内 quoted 中 `\r` 永远是内容（即使后面不是 `\n`）。

## 4. 上限「立刻」
字段字节计数在每个内容字节（含 quoted 中 `""` 合出的单个 `"`、字段内 `\r\n` 两字节）追加时递增，追加**前**检查
MaxFieldBytes，超限立即返回 ErrFieldTooLarge，不先存整条记录。字段数在每收到一个字段时（逗号/行尾）与 MaxFields 比；
记录数在每条完整记录入表前与 MaxRecords 比。命中即终态，后续 Feed 返回同一错误。

## 5. 状态转移表
字节类：C `,`　Q `"`　N `\n`　R `\r`　O 其它

| 状态 | C | Q | N | R | O |
|---|---|---|---|---|---|
| fieldStart | 发空字段→fieldStart | →quoted | 发空字段+行尾→fieldStart | →fieldCR | 存字节→unquoted |
| unquoted | 发字段→fieldStart | ErrQuoteInBare | 发字段+行尾→fieldStart | →fieldCR | 存字节→unquoted |
| quoted | 存逗号→quoted | →quoteSeen | 存\n→quoted | 存\r→quoted | 存字节→quoted |
| quoteSeen | 发字段→fieldStart | 存"→quoted | 发字段+行尾→fieldStart | ErrCharsAfterQuote | ErrCharsAfterQuote |
| fieldCR | — | — | 发字段+行尾→fieldStart | ErrBareCR | ErrBareCR |

Close：quoted/quoteSeen→ErrUnclosedQuote；fieldCR→ErrBareCR；fieldStart/unquoted 发末字段与末记录
（仅当本流发出过字段；末尾终止符后不留空记录）。

## 6. 规范输入
记录以 `\n` 终止；含 `,` `"` `\r` `\n` 的字段及（单列表）末尾空字段加引号，`"` 转义为 `""`；其余裸写。
此集合上 `Write(Parse(x)) == x`。
