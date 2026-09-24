# 推导与不变量

七行表（memCap=2，时间戳=访问序号；写一律先落磁盘）：
1. W(A,1)   内存 {A@1}          磁盘 {A:1}           读数0 返回-
2. W(B,2)   内存 {A@1,B@2}      磁盘 {A:1,B:2}       读数0 返回-
3. R(A)     内存 {B@2,A@3}      磁盘 {A:1,B:2}       读数0 返回1
4. W(C,3)   内存 {A@3,C@4}      磁盘 {A:1,B:2,C:3}   读数0 换出B 返回-
5. R(B)     内存 {C@4,B@5}      磁盘 {A:1,B:2,C:3}   读数1 返回2
6. W(B,20)  内存 {C@4,B@6}      磁盘 {A:1,B:20,C:3}  读数1 返回-
7. R(B)     内存 {C@4,B@7}      磁盘 {A:1,B:20,C:3}  读数1 返回20

(甲) 第4步换出B（时间戳最小@2；并列再按字典序）。若误换成MRU则换出A；第5步R(B)命中内存，读数错成0，正确为1。
(乙) 第7步返回20。若Write只更新磁盘不更新内存，B缓存仍旧值2，第7步错成2。
(丙) Read(D) 返回("",false) 即不存在。若误当零值则错成("",true)。

不变量 → 代码保证位置 → 钉住测试：
1 透明读写：store.Read 命中热层返回hot值、未命中则从cold提升再返回；任序后等于Write重放 — TestReplayConsistency（api_test.go）
2 写穿正确：store.Write 首行即 cold[k]=v，冷key被覆盖也先写磁盘 — TestWriteThroughColdOverwrite（api_test.go）
3 LRU精确：tier.Index 双向链表 Add/Touch(MoveToFront)、Victim取尾（并列用Older按key）；store.promote 超限调 Victim — TestLRUPrecision、TestEvictionScanBounded、TestTierOrdering
4 失败不留痕：api.New 拒 memCap<=0；api.Write 五类校验全部先于 store.Write，拒绝即不触状态 — TestRejectedLeavesNoTrace（api_test.go）
