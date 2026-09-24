1. `pi[0]=0`（空真前缀），`pi[1]=0`（长度 1 无真前缀）；失配后已匹配长度 `k=pi[k-1]`。
2. `ababaca` 的表（长度1..7）：`[0 0 1 2 3 0 1]`。
3. `abababaca` 只有一次失配：i=3、k=3 比较 `t[3]='a'` 与 `p[3]='b'`，k=3→pi[2]=1，文本指针仍停在 3；重比后完整匹配起点 2。
4. 若失配写成 `pi[k-1]-1`：在 i=3 失配时 k=3→0，下轮不再比较 `t[3]='a'` 与 `p[1]='a'`，漏掉起点 2。
5. 若把第 1 项设成 1：首字符失配时 k=1→1 形成不推进文本的循环；若强制比较又会重复消耗文本比较。
6. 不变量1（找全且不多找）：`find.FindAll` 经 `scan.Scanner` 返回完整重叠位置；`TestFindAllNaiveEquivalence` 钉住。
7. 不变量2（文本指针不回退）：`scan.Scan` 每轮只在当前 `i` 比较，末尾固定 `i++`；`TestScanCountersLinear` 钉住。
8. 不变量3（失配表自洽）：`table.Prefix.selfCheck` 逐位验证 `<i` 且前后缀相等；`TestTableSelfCheck` 钉住。
9. 不变量4（失败不留痕）：`find.Compile` 完成校验后才创建对象，`FindAll` 不写入接收器；`TestRejectedOperationsLeaveNoTrace` 钉住。
