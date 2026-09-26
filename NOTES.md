# NOTES（n=3，多数派=2；p=(p0,p1,p2)；各列为 (accepted,value)，未接受=(0,-)；C=Chosen）

S1 Prepare(0,2)：p=(2,0,0)；a0(0,-) a1(0,-) a2(0,-)；C=无
S2 Prepare(1,2)：p=(2,2,0)；a0(0,-) a1(0,-) a2(0,-)；C=无
S3 Prepare(2,2)：p=(2,2,2)；a0(0,-) a1(0,-) a2(0,-)；C=无
S4 Accept(0,2,10)：p=(2,2,2)；a0(2,10) a1(0,-) a2(0,-)；C=无（10 仅 1 票）
S5 Accept(1,2,10)：p=(2,2,2)；a0(2,10) a1(2,10) a2(0,-)；C=10
S6 Prepare(0,3)：p=(3,2,2)；a0(2,10) a1(2,10) a2(0,-)；C=10
S7 Prepare(1,3)：p=(3,3,2)；a0(2,10) a1(2,10) a2(0,-)；C=10
S8 Prepare(2,3)：p=(3,3,3)；a0(2,10) a1(2,10) a2(0,-)；C=10
S9 Accept(0,3,10)：p=(3,3,3)；a0(3,10) a1(2,10) a2(0,-)；C=10
S10 Accept(1,3,10)：p=(3,3,3)；a0(3,10) a1(3,10) a2(0,-)；C=10

(甲) 正确实现：2<3 拒绝，a2 保持 (0,-)，全机状态不变。错判成 n>=accepted：2>=0 为真，a2 错成 (2,99)（promised=3 之下的伪接受）。99 暂未达多数派、Chosen 仍 10，但第 4 轮 Prepare 会把 (2,99) 当已接受回报，若其号最大将迫使提案者选 99，污染最终选中值：违反不变量4（失败不留痕）并危及不变量3。
(乙) 第 3 轮多数派回报为 (2,10)、(2,10)、(0,-)，已接受号最大的值是 10，S9/S10 必须 accept 10。若始终自用 20：a0、a1 改接受 20，最终 Chosen=20（正确为 10）；10 在 S5 已被选中却被改写，违反不变量3（安全性）。
(丙) 错写成严格 n>promised：2>2 为假，S4 被拒，a0 的 accepted 错为 0（正确应为 2）。全新一轮中所有 accept 都因 n==promised 被拒且无报错，任何值都无法接受、Chosen 永久为无（活性死锁）。

不变量落点（位置 / 钉住的测试）：
1. Chosen=朴素重算：每次成功 Accept 在 quorum.Cluster.Accept 里增量迁移计数 counts（旧值--、新值++），Cluster.Chosen 只读 counts 不扫 acceptor；测试 TestChosenMatchesNaiveRecompute。
2. 簿记自洽：acpt.Prepare 仅在 n>promised 时升 promised（单调），acpt.Accept 仅在 n>=promised 时置 accepted=n，故恒有 promised>=accepted（题面 accepted>=promised 与 S6 的 p=3、accepted=2 矛盾，按给定语义取此方向）；测试 TestBookkeepingInvariant。
3. 至多一个值被选中：任意两个多数派必有公共 acceptor，而每个 acceptor 同时只持一个值，Chosen 由增量 counts 判定；测试 TestSafetyAtMostOneValue。
4. 失败不留痕：api 先做下标/提案号校验再交 acpt 比较判定，拒绝路径无任何赋值；Accept 的 n<promised 为正常拒绝返回 false；测试 TestRejectionLeavesNoTrace。
