# NOTES — ontology-294 writeset parallel replay

八事务推导（P=2；"-" 表示该键此前无写者；读集不参与依赖）：
T1 依赖{}; a:-; 深1; 轮1
T2 依赖{}(R{a}不计); b:-; 深1; 轮1
T3 依赖{1}; a:1 c:-; 深2; 轮2
T4 依赖{}(R{c}不计); d:-; 深1; 轮2
T5 依赖{2,4}; b:2 d:4; 深2; 轮3
T6 依赖{3}; c:3; 深3; 轮3
T7 依赖{}(R{a}不计); e:-; 深1; 轮4
T8 依赖{5,7}; b:5 e:7; 深3; 轮5
总轮5；最大并行4（无上限时轮1={1,2,4,7}，其后{3,5}、{6,8}）；最终值 a=3 b=8 c=6 d=5 e=8。
(甲) T8 会错成2：实现只取 Seq 最大的相交者 T7(深1)+1；正确=1+max(深T5=2,深T7=1)=3。同键写者链上每个新写者必依赖上一写者，深度沿链严格递增，故每键最后写者的深度已是该键全部写者最大者；跨键取 max 即全部相交者 max，只需记每键最后写者。
(乙) 读写冲突也算依赖时深度=1,2,3,4,5,6,4,7，最大7（额外边：T2→1(R{a})；T3→2(W∩R2={a})；T4→3(R{c})；T6→2,4,5(R{b})与Wc∩R4；T7→3(R{a})；T8→6(W∩R6={b})，另含全部WW边）。
(丙) 逐层调度轮次=1,1,3,2,3,4,2,4，总4轮。若把"依赖在之前轮次完成"误作"本轮或之前已安排"：轮4={7,8}，总轮数4；违反不变量2——T7 与 T8 写集交于 {e} 却同轮（要求轮(T8)>轮(T7)）。

不变量保证位置 / 钉住测试：
1 与朴素一致：dep/dep.go Add() 对去重写集每键只查 last 写者取深度 max+1；TestDepthMatchesNaive
2 调度合法：sched/sched.go Build() 要求依赖轮次严格<当前轮、每轮≤P、每事务恰好一次；TestScheduleLegalAndReplay
3 回放等价：sched/sched.go Replay() 同轮多 goroutine 真正并发、轮间 WaitGroup 屏障；TestScheduleLegalAndReplay 与 TestConcurrentReplay（八事务向量另由 TestEightTxnSpec 钉住）
4 失败不留痕：api/api.go Append() 先整批 Validate 全通过才 Add，plan 仅成功后 Build；TestAppendRejectionAtomic
复杂度：dep/dep.go 非导出字段 checks，白盒 TestCheckCounter 断言不随 m 增长；CheckCostBound 内部复核只返回 error，不泄露数值。
