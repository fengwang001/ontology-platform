# 设计推导（CSV RFC4180 方言，半包续传 + 并行切分）

## 1. 空行与单列空值

选择：**空行（连续两个行终止符）被跳过**，不是一条记录。理由：
RFC4180 明确忽略 CRLF 间的空行；且把 `a\n\nb` 解释成 3 条记录会让
「文件末尾换行」和「空白」凭空造出数据。

那么单列空值记录怎么表示？解析器只在两种情形产出字段：逗号分隔或行终止。
空行若被跳过，单列、值为空的唯一编码只能是**显式引号空字段 `""`**。
因此回写规则必须加一条：**单列表中的空字段（无论标记如何）必须写成 `""`**，
否则 `Write` 出空串，再 Parse 会被当作空行跳过，记录丢失，往返失败。
多列表中的空字段写空串即可（逗号仍确定其存在）。

## 2. par 切点落在引号内

切点可落在任意字节，段起点无法自证是否在引号内。方案：
- 先用一遍**只跟踪引号深度奇偶**的顺序扫描确定每个切点边界状态
  （引号外 / 引号内 / 引号内刚见引号 / CR 待定）。这一遍每字节恰好一次，
  O(N)，同时得到各段起始的记录号、字段号、前一段悬而未决的字段。
- 各 worker 从真实边界状态起解析本段；故每字节在「预扫描 + 精解析」
  各处理一次，总计 ≤ 2N + O(K)，不会因长引号字段反复重扫。
- 每段结束返回 Tail：末状态 + 尚未闭合字段（跨切点时 Start 为全局起点）。
  拼接时若下段仍在同一字段内则合并字段值与引号标记，否则下段先产出该字段。
- 段内事件携带段内偏移，加段起点换算全局偏移；记录号/字段号由边界预扫描
  的累计计数加段内增量得到。错误同理换算，与单线程逐位相同（状态机是纯
  函数：给定 (state, byte) 结果唯一）。

## 3. CR 待定状态

未引号字段中见 `\r` 不能立刻判定行尾（孤立 `\r` 属错误）。状态机进入
`crPending`：缓冲 `\r`，暂不计入字段。下一字符若为 `\n`，行尾成立，
`\r` 不属字段内容；若为其他字节（含 EOF），报 ErrBareCR。半包续传时
该状态天然保存在解析器状态里，下次 Feed 继续；流在 `\r` 处 Close，按
「其后非 \n」判 ErrBareCR。par 切点可落在 `\r\n` 之间：边界状态记为
`crPending` 作下段起始状态，行为与续传一致。引号字段内不设此态，
`\r` 直接作内容保留。

## 4. 上限「立刻」

字段字节计数 = 追加进字段缓冲的内容字节数：未引号字段每个普通字节 +1；
引号字段内每个内容字节 +1，转义 `""` 的第二个引号代表一个内容引号，
故 **+1（不是 +2）**。每追加一个字节前先判断，第 (Max+1) 个内容字节
到达的当次 Feed 即返回 ErrFieldTooLong，不先攒整条记录。字段数在字段
闭合时自增并立即比 MaxFields；记录数在记录闭合时立即比 MaxRecords。
越限后进入终态，记住错误，后续 Feed/Close 返回同一错误。

## 5. 状态转移表

输入类别：`,`=`C`；`\n`=`L`；`\r`=`R`；`"`=`Q`；其他=`X`。
动作：ef=结束字段(Start,End=偏移,Quoted)；er=结束字段并结束记录；
skipBlank=空行跳过；cnt=字段字节+1（超限即错）。

| 状态 \ 输入 | C | L | R | Q | X |
|---|---|---|---|---|---|
| fStart | ef→fStart | 空行skipBlank→fStart | →crPending(记偏移) | →inQuote(Q=true) | cnt→inField |
| inField | ef→fStart | er→fStart | →crPending | **ErrBareQuote** | cnt→inField |
| inQuote | cnt→inQuote | cnt→inQuote | cnt→inQuote | →quoteSeen | cnt→inQuote |
| quoteSeen | ef→fStart | er→fStart | →crPendingAfterQuote | →inQuote(cnt+1) | **ErrCharAfterQuote** |
| crPending | ef→fStart | er→fStart | **ErrBareCR** | **ErrBareCR** | **ErrBareCR** |

crPendingAfterQuote 与 crPending 同表（字段已闭合，遇非 L 报 ErrBareCR）。
Close：inField/fStart 有悬置字段→产出末字段并结束记录；inQuote→
ErrUnterminated；crPending*→ErrBareCR。末尾换行不产生额外记录。

## 6. 规范输入与往返

规范输入：UTF-8 字节；记录间恰一个 `\n`，末尾无 `\n`；字段仅在含
`,` `"` `\r` `\n` 或「单列表空字段」时加引号；引号写作 `""`。
在此集合上 `Write(Parse(x)) == x`（引号字段内原有 `\r\n` 内容原样保留）。

单实例解析器非并发安全；par 内部 K goroutine 并发，段间无共享可变状态。
