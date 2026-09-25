# PCP 推导与不变量
设定：T1=5,T2=3,T3=1；Use 后 ceil(Rx)=max(5,1)=5，ceil(Ry)=max(3,1)=3。
| # | 操作 | 结果 | 系统天花板 | 执行者/T3 有效优先级 |
|1|Acquire(T3,Rx)|granted|5|T3=5 / T3=5|
|2|Acquire(T2,Ry)|blocked(3不大于5)|5|T2=3 / T3=5|
|3|Acquire(T1,Rx)|blocked(严格>，5不大于5)|5|T1=5 / T3=5|
|4|Release(T3,Rx)|—|0|T3=1|
|5|Acquire(T1,Rx)|granted|5|T1=5 / T3=1|
|6|Acquire(T2,Ry)|blocked(3不大于5)|5|T2=3 / T3=1|
|7|Release(T1,Rx)|—|0|T1=5 / T3=1|
|8|Acquire(T2,Ry)|granted|3|T2=3 / T3=1|
(甲) 若取 min：ceil(Rx)=1、ceil(Ry)=1；第2步 3>1 被错判 granted，T2 持 Ry 后抢占 T3，T1 在第3步仍等 Rx——中优先级 T2 插到 T1 前，无限优先级反转。
(乙) 若条件写成 >=：第3步 5>=5 错判 granted，但 Rx 仍在 T3 手中→Rx 同时被 T3、T1 持有，违反不变量2（互斥）。
(丙) 第1步后 EP(T3)=max(1,5)=5；若不抬升：T2(3) 抢占 T3(1)，T1(5) 等 Rx 实际等被 T2 压住的 T3——经典无限优先级反转（PCP 正是解药）。
## 不变量落点
1. 与朴素参照一致：lock.Acquire 用增量 othersCeiling 判定，由 api_test.TestNaiveReference 随机序列逐操作对拍钉住。
2. 互斥：lock.Acquire 先查 holder[res]，授予才写入；TestEightStepSequence 与 TestConcurrentMutex 钉住。
3. 天花板正确：ceil.Table.Declare 增量取 max；lock.EffectivePriority 取 max(base,持锁天花板)；api.TestEightStepSequence（含内置 SelfCheck 完整八步）钉住。
4. 失败不留痕：lock 全部参数校验先于任何状态写入；api.TestSentinelErrorsNoTrace 逐类断言状态快照不变且可继续使用。
