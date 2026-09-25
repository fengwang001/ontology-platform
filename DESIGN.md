# 设计推导

## 一、空行与单列空值
选择：空行（`\n` 或 `\r\n` 前没有任何字段内容）被跳过，不产生记录。
推导：否则两个连续换行无法区分"空行"与"单列空记录"；跳过空行后，单列空记录
必须与空行可区分——单列表中唯一字段为空时必须写成 `""`（Quoted=true）。
不加引号时 `a\n\n` 只会解析出记录 a，紧跟空行被跳过，空值丢失。故 writer 对
"单列表的空字段"恒加引号。表头=第一条非跳过记录；列数按它校验。

## 二、CR 待定状态 sCR
裸字段末尾见 `\r` 进 sCR：下一字节为 `\n` 则记录结束；否则孤立 `\r` 报错
（偏移指回该 `\r`）。故 `\r` 到达时不发射 Cell、不计入值。流在 sCR 结束（Close）
等同孤立 `\r`。切分点在 `\r`/`\n` 之间：`\r` 留在本段结束快照，par 拼接时由
下一段首字节决定（见四），不会在段边界误报。引号内 `\r` 是普通内容立即入账，
随后 `\n` 同样入账（`\r\n` 原样保留）。

## 三、第7条"立刻"计数
裸字段每追加 1 字节长度 +1，达到上限+1 的字节到达即 ErrFieldTooLarge。
引号字段中 `""` 转义在第二个引号到达时只 +1（此刻检查）；其余每字节 +1。
sCR 的 `\r` 未入账无需回退。检查在状态机内、Cell 发射之前，不缓冲整记录；
出错即终态，已发射记录保留，后续 Feed 返回同一错误。

## 四、par 切点（任意偏移：引号内、`""` 中间、`\r\n` 中间）
每段 worker 跑两假设：H0=起点在"字段开始、引号外"；H1=起点在 sQuote
（左边界有被切开的字段，标记 headOngoing）。顺序拼接时按前段结束状态 E 选：
E∈{sStart,sBare} 用 H0；E∈{sQuote,sQQuote} 用 H1。sCR 时 H0 无法表达悬而未决
的 `\r`：让机器以 sCR 启动并把下段首字节作"重叠字节"重放（偏移 base-1），
用 overlapLen 截掉 H1 首 Cell 的重复值前缀。引号外只可能 sStart/sBare/sCR，
引号内只可能 sQuote/sQQuote，假设集完备；拼接确定性 => 与单线程逐事件相同。
坐标换算：偏移=base+local；记录号/字段号累加前段计数（空行不计数，计数器即
真实编号；H1 首字段沿用前段开放字段编号）。处理次数上界 2n+2K（每段两假设，
重叠字节每段至多 1 个），不因长引号字段退化。

## 状态转移表
状态 \ 输入 | `,` | `\n` | `\r` | `"` | 其他
---|---|---|---|---|---
sStart 字段开始/引号外 | emit 空 Cell→sStart | 空行跳过,RecordEnd | →sCR | ErrBareQuote | 值+1→sBare
sBare 裸字段中 | emit Cell→sStart | emit Cell,RecordEnd | →sCR | ErrBareQuote | 值+1→sBare
sQuote 引号字段中 | 值+1 | 值+1 | 值+1 | →sQQuote | 值+1
sQQuote 刚见引号 | emit Cell→sStart | emit Cell,RecordEnd | emit Cell→sCR | 值+1(转义)→sQuote | ErrQuoteClose
sCR 待定 | （sCR 启动重放见 `,`：emit Cell→sStart） | RecordEnd | — | ErrBareQuote | ErrLoneCR

Close：sQuote/sQQuote→ErrUnterminated（偏移=开引号）；sCR→ErrLoneCR；
其余：有开放字段则 emit Cell；本记录出现过字段则 RecordEnd（末尾换行不补空记录）。
Cell.Start=首字节偏移（引号字段为开引号偏移），End=最后内容字节偏移+1。
writer 必要引号：值含 `,` `"` `\r` `\n`，或单列表空字段；`"`→`""`。
记录间 `\n`，非末记录后均写 `\n`（末尾无换行）。
规范输入：UTF-8 字节流，记录以 `\n` 分隔（无 `\r\n`、无空行、无末尾换行），
仅必要时加引号，空单列记录写 `""`。
单流式 Lexer 实例非并发安全；par 内 K 个 goroutine 各自独立机器，-race 安全。
