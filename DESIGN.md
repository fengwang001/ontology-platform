设计推导（推导过程 + 结论）

1. 空行与单列空值
推导：空行（`\n\n` 中的第二个 `\n`，或文件开头的 `\n`）若当作「一条含一个未引号空字段的
记录」，则「空记录」与「单列空值记录」记法完全相同，回写器无法区分二者，必然丢数据。
故选择：空行 = 被跳过，不产生记录；单列空值必须显式写作 `""`（带引号空字段）。
判空行时机：某条记录只含 1 个字段、该字段空且未加引号、且字段起点字节处就是记录结束符，
则丢弃该记录；多列记录中的未引号空字段（如 `a,,b`）是真实字段，保留。
回写「必要引号」：内容含 `,` `"` `\r` `\n` 时；或单列表唯一字段为空（否则与空行不可区分）。
引号内 `"` 写成 `""`。规范输入定义：无空行、每条记录都以 `\n` 结束、行内只用必要引号。

2. par 切点（可能落在引号字段、`""`、`\r\n` 中间）
方案：切点任意，每段 worker 以「段起点在引号外」假设跑一遍核心状态机；该遍在段内第一次
到达「引号外」之前的事件都是猜测前缀（首字节就被引号围住的整段可能全程无界标）。
拼接从左到右：前一段的出口快照（state / 已起始字段状态 / 计数器）确定下一段真实入口；
若真实入口为「引号内」而该段只算了「外」假设，则以「内」入口重算该段（每段至多 2 遍，
故字节处理 ≤ 2N+常数，不反复重扫）。状态机本身是字节增量、无回看，段 = 喂一段字节，
因此重放与整段单线程完全等价；事件坐标是段内局部值，加段起点偏移即全局字节偏移，
记录号/字段号加上入口快照里已完成（含进行中）的计数即全局号。错误同样由核心机产生，
位置随上述换算，故错误也完全相同。

3. CR 待定
未引号字段见到 `\r`：可能是行尾（后随 `\n`）或孤立 CR（错误）。进入 crPending，不立即
落任何动作，并保存该 `\r` 偏移；下一字节为 `\n` 才结束记录（偏移取 `\r` 处），否则报
ErrLoneCR。半包续传时 crPending 跨 Feed 保存即可；流在 `\r` 处 Close，无下一字节，判
ErrLoneCR。par 切点可正落在 `\r`：它只作为出口快照 crPending 传给下一段，无需特殊处理。
引号字段内 `\r` 是内容（含 `\r\n` 原样保留），不进入 crPending。

4. 上限「立刻」
字段字节数 = 逻辑内容字节，引号定界符不计，`""` 转义只算 1 个字节（即只在第二个引号被
识别为转义时不计数）。每个内容字节进入时即比较，第 MaxFieldBytes+1 个字节立刻返回
ErrFieldTooLarge，不缓冲整条记录。MaxFields 在第 limit+1 个字段开始时立即报；
MaxRecords 在第 limit+1 条非空行记录开始时立即报。终态后 Feed/Close 返回同一错误。

状态转移表（F=新字段/字段续，R=结束记录，P=暂存CR，Q=内容引号，e=错误，len=立即查上限）
| 状态 | , | " | \n | \r | 其他 |
| fieldStart | F+；见"入quoted | e BadQuote | R(空行则丢弃) | crPending P | unquoted,len |
| unquoted | F+ | e BadQuote | R | crPending P | 续,len |
| quoted | 续,len | quoteSeen | 续,len | 续,len | 续,len |
| quoteSeen | F+ | Q(+1),len | R | crPending P | e CharAfterQuote |
| crPending | 收到 \n→R；收到其他→e LoneCR 并重处理该字节 |
特例：空输入零记录；末尾无换行时 Close 只要有已起始字段（含空 `""`）就 R 收尾；EOF 处
quoted/quoteSeen 为 e UnterminatedQuote；EOF 处 crPending 为 e LoneCR。

错误（可判定，均带 Offset/Record/Field）：ErrBadQuote、ErrCharAfterQuote、
ErrUnterminatedQuote、ErrLoneCR、ErrFieldCount、ErrFieldTooLarge、ErrTooManyFields、
ErrTooManyRecords、ErrTerminal。偏移=错误字节全局偏移；EOF 类报输入总长度。
单一流式实例非并发安全。
