# CSV 方言解析器设计

## 推导一：空行与单列空值
记录的物化为「遇到换行」或「EOF 时已有内容」。选择：**空行也是一条记录，含一个未引号空字段**（空文件=0 条记录，`"\n"`=1 条，`"a\n\n"`=2 条）。理由：空行被跳过会让 `\n\n` 无法表达空值记录，且跳过者靠列数也无法区分「列内空」与「行不存在」。空字段是惰性的：首字节若为 `\n`/`\r`，先补开一个字段再收行。往返推论：**单列、唯一字段为未引号空值时，记录必须写成 `""`**；否则该记录写出来是空行，与「该列没有数据、仅有行分隔」的字节不可区分，回读即丢数据。多列记录里的空字段天然由相邻逗号定界，保持不加引号。

## 推导二：par 切点
每段对**两种入口假设**各完整解析一遍：入口在引号外（FRESH/BARE，由事件是否已开字段区分）与入口在引号内（QUOTED；另对「上一段恰停在转义引号」保留 QESC 情形，仅消费首字节后并入上述二者）。左段出口状态（Fresh/Bare/Quoted/Qesc/CR/None）唯一决定右段应选哪遍：Fresh→外部假设并丢弃其惰性开字段事件（首字节是逗号/引号）或保留（是数据）；Bare→外部假设并丢弃其首字节触发的开字段；Quoted→内部假设，首字节作为字段数据；CR→下一段首字节为 `\n` 时补一条 Record 事件，否则即孤立 `\r` 错误。拼接按事件的全局偏移线性串接，故字节偏移天然全局；记录号/字段号由 table 在拼接后的事件流上统一编号。任何一段报错：整体回退为对整缓冲做一次流式解析，取其权威错误与前缀，因此错误坐标恒等于单线程结果，且双假设每字节至多两遍。

## 推导三：CR 待定
未引号字段遇 `\r`：字段先闭合，进入 CRPEND，但**不发 Record**。下一字节是 `\n` 才发 Record；是其他任意字节（含逗号、`\r`、数据）即 ErrBareCR，偏移指向 `\r`。半包：该待定态跨 Feed 保存，`Close` 时仍待定则判 ErrBareCR（`\r` 后永远不会有 `\n` 了）。par：`\r` 落在段尾时出口状态为 CR，由左段携带到右段按同样规则处理；`\r` 恰为全缓冲最后字节时，最后一段 flush 报 ErrBareCR。引号字段内 `\r` 是普通数据字节，无待定态。

## 推导四：上限的「立刻」
字段字节在**并入字段内容的同一时刻**累加并比较 MaxFieldBytes：未引号字段每个普通字节 +1；引号字段内普通字节（含 `\r`）+1，`""` 转义对在见到第二个 `"` 时并入 1 个字节。这样第一个超限字节到达即 ErrFieldTooLong，从不先攒整字段。MaxFieldsPerRecord 在每次开字段时检查；MaxRecords 在每条记录物化时检查。触发后解析器进入终态，所有后续 Feed/Close 返回同一错误。

## 状态转移表（c=字节类别；惰性开字段=发 Open）
| 状态 | 普通 | `,` | `"` | `\n` | `\r` | EOF |
|---|---|---|---|---|---|---|
| FRESH 字段前 | BARE+Open,Data | Open,Close→FRESH | QUOTED+Open | Open,Close,Record→FRESH | Open,Close→CRPEND | Open,Close,Record(尾记录) |
| BARE 未引号中 | Data | Close→FRESH | ErrBareQuote | Close,Record→FRESH | Close→CRPEND | Close,Record(尾记录) |
| QUOTED 引号中 | Data | Data | QESC | Data | Data | ErrUnclosedQuote |
| QESC 刚见引号 | (继续 QUOTED) Data+`"` | Close→FRESH | Data+`"`→QUOTED | Close,Record→FRESH | Close→CRPEND | Close,Record(尾记录) |
| CRPEND | ErrBareCR | ErrBareCR | ErrBareCR | Record→FRESH | ErrBareCR | ErrBareCR |

四类语法错误：ErrBareQuote（未引号字段出现 `"`）、ErrQuoteAfterClose（如 `"ab"c`，QESC 后见普通字节）、ErrUnclosedQuote（EOF 仍在 QUOTED）、ErrColumnCount（记录列数≠首条，Record 时判）；另有 ErrBareCR。错误携带 Offset（从 0）、Record、Field（从 1）。

## 回写与规范输入
回写逐字段保留 Quoted 标记；仅在必要时强制加引号：含 `,`、`"`、`\r`、`\n`，以及单列空值（推导一）。引号字段内 `"`→`""`。**规范输入**定义：记录间一律 `\n`，末记录后带一个 `\n`，且仅在必要时加引号；此时 `Write(Parse(x))==x` 逐字节成立。
