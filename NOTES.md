# 推导与不变量

七步表（创世 Seq=0，初始条目总数 1）：

| 步 | 请求 | 接受/拒绝 | 分配 Seq | 步后总数 |
|---|---|---|---|---|
| 1 | (10,"A","put") | 接受 | 1 | 2 |
| 2 | (12,"A","get") | 接受（12≥10） | 2 | 3 |
| 3 | (12,"B","del") | 接受（12=12，非递减允许相等） | 3 | 4 |
| 4 | (9,"A","put") | 拒绝：TS 9 < 前条 12（乱序） | 无 | 4 |
| 5 | (15,"B","put") | 接受 | 4 | 5 |
| 6 | (15,"","get") | 拒绝：Who 为空 | 无 | 5 |
| 7 | (20,"A","del") | 接受 | 5 | 6 |

(甲) 第 3 步被接受。若误写成严格递增（TS>前条），第 3 步被拒且不消耗 Seq；
则第 5 步错拿 Seq=3（应 4），第 7 步错拿 Seq=4（应 5）。
(乙) 正确实现第 5 步拿 Seq=4。若被拒 Append 也耗号：第 4 步空耗 Seq=4，第 5 步错拿
Seq=5，日志在 Seq=3 与 5 之间留下空洞 Seq=4（无任何条目，连续性被破坏）。
(丙) 仅改 Seq=3 的 Op(del→put)、不改任何存储 Hash：Verify 从创世连续重算，在 Seq=3
首次失配，返回 3；Affected(3)=[3,4,5]。成对校验只用前驱的**存储** Hash 重算，
Seq=3 的存储 Hash 未变，故 Seq=4、5 仍各自"对得上"而漏报：漏掉 4、5。

不变量（代码保证位置 / 钉住的测试函数）：
1. Seq 连续无空洞、TS 非递减：log/audit.go 的 Append（先校验、seq=len(entries) 分配）；
   测试 TestSevenSteps、TestRandomAppendOrder。
2. Verify 重算定位最小被篡改 Seq：audit.go Verify 以**重算**哈希连续传播而非读存储值；
   测试 TestVerifyTamper。
3. 哈希链自洽、任意前缀一致、O(1) 链头接续：ent/ent.go NextHash、audit.go 的 head
   缓存与非导出 reads；测试 TestHeadReadsConstant、TestSelfCheck、TestPrefixes。
4. 失败不留痕：Append 全部校验通过后才改状态；测试 TestSentinelErrors、TestSevenSteps。
