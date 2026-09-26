# 因果广播 + 向量时钟：推导与不变量

七步（n=3，初始全 0；每行：该步结果 | VC[0] / VC[1] / VC[2]）：
1. S1 Broadcast(0)=M1(0,[1,0,0])：广播（自发自投） | [1,0,0] / [0,0,0] / [0,0,0]
2. S2 Deliver(1,M1)：投递 | [1,0,0] / [1,0,0] / [0,0,0]
3. S3 Broadcast(1)=M2(1,[1,1,0])：广播 | [1,0,0] / [1,1,0] / [0,0,0]
4. S4 Deliver(2,M1)：投递 | [1,0,0] / [1,1,0] / [1,0,0]
5. S5 Deliver(2,M2)：投递 | [1,0,0] / [1,1,0] / [1,1,0]
6. S6 Broadcast(1)=M3(1,[1,2,0])：广播 | [1,0,0] / [1,2,0] / [1,1,0]
7. S7 Deliver(0,M3)：阻塞（ts[1]=2 ≠ VC[0][1]+1=1） | [1,0,0] / [1,2,0] / [1,1,0]

(甲) VC[2]=[0,0,0] 时 ts[0]=1 > VC[2][0]=0，跨节点检查失败：正确实现阻塞，M2 留缓冲、状态不变；若忽略 k!=q 检查，M2 被错误投递、VC[2]=[0,1,0]，未收 M1 先收 M2，违反不变量 2（因果序；从零朴素重放同样 diverge，亦违反 1）。
(乙) S5 后正确 VC[2]=[1,1,0]；若误把自己分量（节点 2）加 1，则错成 [1,0,1]，违反不变量 1。
(丙) S7 正确实现阻塞：节点 0 的 VC[0][1]=0，下一条应是 M2（节点 1 的第 1 条），M3 卡在 M2 之后；若条件错写成 ts[q]>VC[p][q]，则 2>0 成立、M3 被投递，跳过 M2（VC[0][1] 由 0 跳到 1），违反不变量 3，且此后 M2 永不可投递。

不变量 → 代码保证位置 / 钉住的测试函数：
1. 朴素重算一致：causal/causal.go 的 Broadcast/Deliver 只推进发送者分量且 Deliver 必经 vc.Deliverable 门控；TestInvariantsFuzz（独立模型逐操作重算比对全部 VC）与 SelfCheck。
2. 因果序：vc/vc.go Deliverable 中 `k!=q → ts[k]<=p[k]` 的跨节点上界检查，经 causal.Deliver 门控；TestSevenSteps（S5 依赖满足、S7 阻塞）。
3. 同源不跳号：vc/vc.go Deliverable 首条件 `ts[q]==p[q]+1`；TestInvariantsFuzz（模型断言只逐条交付）与 TestSevenSteps。
4. 失败不留痕：causal/causal.go 越界/自投/未知三处校验都在任何状态写入之前 return；TestFaults（拒后全节点 VC 快照逐字段相等，且随后正常操作成功）。

lastCheck 是 causal 包非导出字段，不出现在任何公开接口；TestLookupConstant 以同包白盒测试直读该字段，证明 O(1) 定位。
