# TOB 定序器推导与不变量
十行表（d=deliveredUpTo, n=nextSeq, 集合=已分配未投递；崩溃后为空洞）：
1 Propose A        -> d=0 n=2 pending={1}
2 Propose B        -> d=0 n=3 pending={1,2}
3 Deliver          -> d=1 n=3 pending={2}
4 Propose C        -> d=1 n=4 pending={2,3}
5 Deliver          -> d=2 n=4 pending={3}
6 Propose D        -> d=2 n=5 pending={3,4}
7 Propose E        -> d=2 n=6 pending={3,4,5}
8 Crash            -> d=2 n=6 空洞={3,4,5}
9 RePropose 3C 4D 5E -> d=2 n=6 pending={3,4,5}
10 Deliver x3      -> d=5 n=6 pending={}
甲: 崩溃后 d=2、n=6，空洞 3/4/5；补发必须沿用 seq 3=C,4=D,5=E。若误当新 Propose 则 C=6、D=7、E=8；seq3 槽位永空，第一个永远投不出去的是 3，6/7/8 也被其堵死。
乙: 乱序补发 D(4),C(3),E(5)，按 seq 投仍为 C,D,E。若按补发到达顺序投，第10步三次 Deliver 依次投出 D,C,E；与正确 C,D,E 相比第1、2条颠倒（乱序）。
丙: 正确实现第10步后共投 5 条：A,B,C,D,E。若 d 回退为 0：第10步投出 A,B,C，A/B 被重复投递（继续抽干会把 A..E 全重投，全程 7 次投递事件），D/E 在第10步丢失。若跳过空洞从 n=6 继续：C,D,E 永久丢失，三次 Deliver 目标 6/7/8 均不存在，全部空返。
不变量（保证位置 / 钉住的测试）：
I1 投递恒为 1..d 连续前缀：tob.(*Log).Deliver 仅按下标 d+1 直接取槽，槽空即不推进游标 / TestInvariantContiguousPrefix
I2 与朴素重放一致：Crash 只截掉 seq>d 的槽、RePropose 按原 seq 回填空槽，日志内容即重放日志 / TestReplayEquivalence
I3 序号单调不回退：seq.Allocator.Alloc 只做 next++，d 只在成功 Deliver 后 ++，补发 seq 恒 < next / TestMonotonicAndOriginalSeq
I4 失败不留痕：四类哨兵错误均在任何写操作之前返回（tob Propose/RePropose、api 透传）/ TestRejectedOpsLeaveNoTrace
