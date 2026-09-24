# NOTES

八步表（maxRows=4；初始 PK1{name:ann,qty:3} PK2{name:bob,qty:5} PK3{name:cid}）
1 Seq1 Update PK1 前像相符→已应用；副本 PK1{ann,4} PK2{bob,5} PK3{cid}；冲突0
2 Seq2 Update PK2 现qty:5≠前像qty:6→前像不符；副本不变；冲突1
3 Seq3 Delete PK3 现缺qty列≠前像qty:""→前像不符(缺列≠空串)；副本不变；冲突2
4 Seq4 Insert PK2 主键已存在→行已存在；副本不变；冲突3
5 Seq5 Delete PK1 现qty:4≠前像qty:3→前像不符；副本不变；冲突4
6 Seq6 Update PK4 主键不存在→行不存在；副本不变；冲突5
7 Seq7 Insert PK4→已应用；副本加 PK4{dan,2}(4行≤4)；冲突5
8 Seq8 Update PK4 前像相符→已应用；终态 PK1{ann,4} PK2{bob,5} PK3{cid} PK4{dan,3}；冲突5
甲(U/D都不较前像)：2应用PK2→{bob,7}、3删PK3、5删PK1；终态 PK2{name:bob,qty:7} PK4{name:dan,qty:3}；冲突=Seq4行已存在、Seq6行不存在。
乙(仅Delete不较前像)：3删PK3、5删PK1，Seq2仍冲突；终态 PK2{name:bob,qty:5} PK4{name:dan,qty:3}；冲突=Seq2前像不符、Seq4行已存在、Seq6行不存在。
丙(冲突记录后仍应用)：第7步错成「行已存在」(第6步盲插已占PK4)；终态 PK2{name:bea,qty:1} PK4{name:dan,qty:3}；冲突共6条(Seq2,3,4,5,6,7)。

不变量：代码保证位置 / 钉住的测试函数
I1 盲应用一致：rapply.go 的 step 仅在 judge 返回已应用时写 ev.After 或 delete；TestRandomBlindConsistency 用独立盲模型逐事件比对。
I2 判定可复算：judge 严格按 主键存在性→整行前像 的顺序取首个命中；TestRecomputeOrderedRules 用事件前状态独立重算。
I3 冲突零副作用且结果完备：冲突只向日志 append，不碰 rows；TestConflictZeroSideEffect 与 TestConflictLogCompleteStrict 钉住。
I4 失败不留痕：ApplyBatch 先整批校验形状与序号、再在克隆表上模拟、全部成功才一次性提交；TestRejectedBatchAtomic 钉住。
