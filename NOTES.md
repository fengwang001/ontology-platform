# NOTES — ontology-280 key-group assignment & rescale

hash: h=0; 对每 UTF-8 字节 b 执行 h=h*31+b（uint32 回绕）。
u1..u8 的 h = 3676,3677,3678,3679,3680,3681,3682,3683；kg=h mod 10：u1=6 u2=7 u3=8 u4=9 u5=0 u6=1 u7=2 u8=3。

| kg | inst@3 | inst@4 | 迁移 | 键 |
|---|---|---|---|---|
| 0 | 0 | 0 | 否 | u5 |
| 1 | 0 | 0 | 否 | u6 |
| 2 | 0 | 0 | 否 | u7 |
| 3 | 0 | 1 | 是 | u8 |
| 4 | 1 | 1 | 否 | (空) |
| 5 | 1 | 2 | 是 | (空) |
| 6 | 1 | 2 | 是 | u1 |
| 7 | 2 | 2 | 否 | u2 |
| 8 | 2 | 3 | 是 | u3 |
| 9 | 2 | 3 | 是 | u4 |

区间 p=3：0:[0,4) 1:[4,7) 2:[7,10)；区间 p=4：0:[0,3) 1:[3,5) 2:[5,8) 3:[8,10)。
(甲) 迁移键组 3,5,6,8,9；其中 kg5 空，迁移量=4，键为 u8(kg3)、u1(kg6)、u3/u4(kg8/9)。若直接 h mod p：p=3 归属 1,2,0,1,2,0,1,2；p=4 归属 0,1,2,3,0,1,2,3 —— 八 个键全部迁移。
(乙) 起点终点都 floor 时 p=4 区间为 [0,2) [2,5) [5,7) [7,10)：kg2 区间说在 inst1、逐键组公式 inst(2,4)=0（u7 状态被发到 inst1，事件却路由到 inst0）；kg7 区间说在 inst3、公式 inst(7,4)=2（u2 状态发到 inst3，事件路由到 inst2）。
(丙) 正确长度 3,2,3,2；各新实例迁入键组数 0、1(kg3)、2(kg5,6)、2(kg8,9)。若定长 maxP/p=2、余数 2 全给最后一个：[0,2)[2,4)[4,6)[6,10)，相对 p=3 有 8 个键组迁移(2,3,4,5,6,7,8,9)，正确方案只有 5 个(3,5,6,8,9)。

## 不变量落点
- I1 与朴素参照一致：kgrp.go 的 Range/Ranges（ceil 公式）+ rescale.go 的 Check 逐 kg 核对归属与放置；测试 TestRangesMatchNaive、TestOwnerAndPlacement。
- I2 区间完整/不交/长度差≤1：rescale.go Check；测试 TestRangesMatchNaive（含 maxP 1..64 全 p 表驱动，I1/I2 同函数钉住）。
- I3 迁移最小且守恒：rescale.go Rescale 未变桶整桶复用、变化桶整体挂接，MovedKeys 仅累加被搬桶 len；测试 TestRescaleConservationAndMinimal、TestVisitedCounterSublinear。
- I4 失败不留痕：kgrp.go 哨兵错误，rescale.go New/Put/Rescale 先校验后改动（Rescale 先在局部算完再提交）；测试 TestSentinelErrorsRejectClean。
- 并发一致：api.go 用 RWMutex、Ranges 返回副本；测试 TestConcurrentReaders（go test -race）。
