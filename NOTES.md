# RR 调度器 NOTES

时间片表 quantum=4：P1(0,10) P2(1,4) P3(3,3) P4(3,2)；到达落在(片起,片止]的进程先入队，被抢占者排其后面。
1 [0,4)   P1 剩6  未完成（期间 P2,P3,P4 依次入尾，P1 再排尾：Q=P2,P3,P4,P1）
2 [4,8)   P2 剩0  完成@8
3 [8,11)  P3 剩0  完成@11
4 [11,13) P4 剩0  完成@13
5 [13,17) P1 剩2  未完成
6 [17,19) P1 剩0  完成@19
完成时刻：P1=19 P2=8 P3=11 P4=13
(甲) P1 末段 [17,19) 跑 2、完成 19；若一律跑满 quantum，末片多跑 2，错成 21。
(乙) t=4 抢占后正确下一个是 P2（新到达者在被抢占者之前）；放回队首(LIFO)则错成 P1。
(丙) 同于 t=3 到达：正确 P3 先 P4 后（(arrival,pid) 升序）；按 burst 短进程优先则 P4(2) 先 P3(3) 后。

不变量（代码保证位置 / 钉住的测试函数）：
1 与朴素参照一致：rr/rr.go 的 simulate 整块推进 min(rem,quantum)，(起,止] 到达先入队、被抢占者排尾；TestEquivNaive
2 服务守恒（每进程恰被服务 burst）：rr/rr.go simulate 内 served[pid]+=run，仅 rem 归零才算完成；TestConservation
3 完成≥arrival+burst 且互不相同：rr/rr.go 完成时刻=片终点，每片至多一个完成；TestCompletionBounds
4 失败不留痕：rr/rr.go Add 先 proc.New 字段校验，proc.Set.Add 锁内先查重后插入；api.New 拒 quantum≤0；TestRejectedAddLeavesState
计数器 tickSteps：rr/rr.go 非导出字段，Run 起始重置、整块推进恒为 0；同包白盒 TestTickStepsNotLinearInM 直读字段；跨包只经 rr.SelfTestCounter 得到 error，不暴露数值。
