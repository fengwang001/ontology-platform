# Paxos acceptor 状态机笔记

十步表（n=3，多数派=2；pX=k 表 promised[X] 变为 k；各格写 (accepted,acceptedValue)）
S1  p0=2            (0,0)   (0,0)   (0,0)   Chosen=无
S2  p1=2            (0,0)   (0,0)   (0,0)   Chosen=无
S3  p2=2            (0,0)   (0,0)   (0,0)   Chosen=无
S4  acc0接受(2,10)  (2,10)  (0,0)   (0,0)   Chosen=无
S5  acc1接受(2,10)  (2,10)  (2,10)  (0,0)   Chosen=10
S6  p0=3            (2,10)  (2,10)  (0,0)   Chosen=10
S7  p1=3            (2,10)  (2,10)  (0,0)   Chosen=10
S8  p2=3            (2,10)  (2,10)  (0,0)   Chosen=10
S9  acc0接受(3,10)  (3,10)  (2,10)  (0,0)   Chosen=10
S10 acc1接受(3,10)  (3,10)  (3,10)  (0,0)   Chosen=10
(甲) 正确：2>=promised2=3 不成立，拒绝，acc2 仍 promised=3,accepted=0,value=0，不留痕。错判 n>=accepted：2>=0 成立，错成 accepted=2,value=99；它向未来提案者谎报回报 (2,99)，更大集群中某多数派若由它与若干 accepted=0 者凑成，提案者将改提 99 并令其达多数派 → 两值被选中，违反不变量3（且本次过期 accept 改了状态，违反不变量4）。
(乙) 第3轮回报 (2,10)(2,10)(0,0)，已接受号最大=2 → 必须 accept 10。始终自用 20：S9/S10 后 20 占2票，Chosen 错成 20（正确=10；10 已于 S5 选中）→ 两个不同值先后达多数派，违反不变量3。
(丙) 错判 n>promised：2>2 不成立，S4 被拒，acc0 accepted 错留 0（正确=2）、value=0（正确=10）。自己 Prepare 的 n 总等于 promised，每轮同轮 Accept 必被拒 → 永无值被选中，liveness 丧失，十步全程 Chosen=无（正确 S5 起=10）。

不变量保证位置 / 钉住的测试函数：
1 与朴素重算一致：Paxos.Chosen 用 quorum.Tally 增量计票，Paxos.agrees 每步与 quorum.Naive 全表重算比对；TestSelfCheck、TestTenStep
2 簿记自洽：acpt.Promise/Accept 的判定与赋值（注：题面 accepted>=promised 与给定语义矛盾，S1 后 promised=2>accepted=0 即反例；成立的是 promised>=accepted 且 promised 只增不减）；TestBookkeeping
3 至多一个值选中：每 acceptor 只投一票，2*(m/2+1)>m 算术上不可能两个多数派（quorum.Tally），跨轮继承由 quorum.PickValue 选最大 accepted 号保证；TestSafety、TestPickValue
4 失败不留痕：acpt.Promise/Accept 所有拒绝分支都在赋值之前 return；api 的下标/提案号校验先于状态访问；TestRejectionLeavesState
