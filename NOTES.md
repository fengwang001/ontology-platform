# NOTES — key group assignment & rescale（maxP=10）
哈希（h=h*31+b，uint32 回绕；'u'=117）：u1..u8 的 h=3676..3683，kg=h mod 10：
u1→6, u2→7, u3→8, u4→9, u5→0, u6→1, u7→2, u8→3；键组 4、5 为空。
| kg | inst p=3 | inst p=4 | 迁移 | 该组键 |
|---|---|---|---|---|
|0|0|0|否|u5|
|1|0|0|否|u6|
|2|0|0|否|u7|
|3|0|1|是|u8|
|4|1|1|否|（空）|
|5|1|2|是|（空）|
|6|1|2|是|u1|
|7|2|2|否|u2|
|8|2|3|是|u3|
|9|2|3|是|u4|
区间（ceil 边界）：p=3 → [0,4) [4,7) [7,10)；p=4 → [0,3) [3,5) [5,8) [8,10)。
(甲) 迁移键组 3,5,6,8,9；迁移量=4，键为 u1、u3、u4、u8（kg5 空）。若直接 h mod p：
p3 余数 1,2,0,1,2,0,1,2；p4 余数 0,1,2,3,0,1,2,3 —— 八个键全部迁移
（u1:1→0,u2:2→1,u3:0→2,u4:1→3,u5:2→0,u6:0→1,u7:1→2,u8:2→3）。
(乙) floor 边界 p=4 区间变为 [0,2) [2,5) [5,7) [7,10)。与 inst(kg,4) 不符：kg2 区间归 1 而公式归 0
（u7 状态发到实例 1，事件却路由到实例 0）；kg7 区间归 3 而公式归 2（u2 状态发到 3，事件路由到 2）。
(丙) 正确区间长度 3,2,3,2；迁入键组数：i0=0、i1=1（kg3）、i2=2（kg5,6）、i3=2（kg8,9）。
若长度恒取 10/4=2、余数全给最后一个：[0,2)[2,4)[4,6)[6,10)，相对 p=3 有 8 个键组迁移
（kg2,3,4,5,6,7,8,9），正确方案只迁移 5 个（kg3,5,6,8,9）。
## 不变量落点
1. 朴素参照一致：kgrp.go 的 Instance/RangeBounds 同源于 floor/ceil 公式；由 TestRangesMatchNaive、TestEightKeyScenario 钉住。
2. 区间完整不交且长度差≤1：api.go 的 Ranges 经 kgrp.RangeBounds 构造；由 TestPartitionComplete 钉住。
3. 迁移最小且守恒：rescale.go Rescale 只搬归属变化的整桶、计数器随条目自增；由 TestRescaleConservation、TestAccessCounter 钉住。
4. 失败不留痕：三个哨兵错误 ErrInvalidMaxP/ErrInvalidParallelism/ErrEmptyKey 均在改状态前返回；由 TestRejectedOpsAtomic 钉住。
