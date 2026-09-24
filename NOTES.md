# 失配表推导
定义 `pi[i]` 为模式前缀 `p[0..i]` 的最长真前缀且同时是后缀的长度；真前缀要求 `pi[i] <= i`，故起点为 `pi[0]=0`。
长度 1 的前缀只有一个字符，没有非空真前缀，所以 `pi[1]=0`（0 基下标）；失配时 `j=pi[j-1]`，不能减一。
`ababaca` 的 0 基表为 `[0,0,1,2,3,0,1]`；按前缀长度 1..7 为 `0,0,1,2,3,0,1`。
在 `abababaca` 中：先匹配 `ababa`（文本下标 0..4，j=5）；下标 5 处 `c!=b`，j 从 5 回退到 `pi[4]=3`，文本指针停在 5。
随后比较 `p[3]=b` 与 `t[5]=b`，再依次匹配 6..8，得到唯一匹配起点 2。
若回退写成减一：j=5 先错回 4，`p[4]=a!=b`，再到 2，`p[2]=a!=b`，再到 0，`a!=b`，漏掉下标 2 的匹配。
若第 0 项设为 1：j=0 失配会被错置为 j=1，状态大于真前缀长度；本例会错位比较并漏掉起点 2，通用情况下还会使文本指针卡死。

# 不变量落点
1. 找全且不多找：`scan.Scanner.Scan` 逐字符 KMP 产出；`TestFindAllTable` 与 `TestOverlap` 对照 `naiveFind`。
2. 文本指针不回退：`scan.Scanner.Scan` 只在循环头递增 `textAdvances`；`TestCountersLinear` 断言推进数恰为 n、比较数 <=2n。
3. 失配表自洽：`table.Table` 保存表，`find.Matcher.SelfCheck` 逐位验证严格小于前缀长且前缀等于后缀；`TestSelfCheck` 钉住。
4. 失败不留痕：`find.Compile` 在替换前校验空串和上限，`FindAll` 先校验长于文本；`TestRejectionLeavesNoTrace` 验证原匹配器和结果不变。
