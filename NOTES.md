# DRF 推导与不变量索引（Ccpu=Cmem=60，A(1,6)、B(4,1)）
1. ①主导资源：A 比 1/60 与 6/60，6/60 大→Mem；B 比 4/60 与 1/60，4/60 大→CPU（相等时才取 CPU）。
2. ②主导份额：s_A=a_A·6/60=a_A/10；s_B=a_B·4/60=a_B/15。
3. ③令两者相等：a_A/10=a_B/15 ⇒ a_B=3a_A/2（即 a_A=2a_B/3）。
4. ④硬约束：cpu 为 a_A+4a_B=7a_A≤60→a_A≤60/7；mem 为 6a_A+a_B=15a_A/2≤60→a_A≤8。mem 先耗尽，故 a_A=8、a_B=12；cpu=8+48=56≤60；份额 8/10=12/15=4/5。
5. (甲) 错成平分单位 a_A=a_B=a：mem 7a=60→a=60/7≈8.57；按 max 的真份额 A=6/7≈0.857、B=4/7≈0.571；B 比 DRF 最小份额 4/5 低 8/35≈0.229，cpu 仅耗 300/7≈42.9。
6. (乙) 错取 min 当主导：A 误判 CPU(1/60)、B 误判 Mem(1/60)，份额相等⇒单位各 60/7；按 max 的真份额仍为 6/7、4/7，B=4/7<4/5 被压低。
7. (丙) 错把 cpu 当绑定：7a_A=60→a_A=60/7、a_B=90/7≈12.86；代入 mem=360/7+90/7=450/7≈64.29>60，超出 30/7≈4.29，违反 mem 硬约束。

## 四条不变量：代码位置与钉住的测试函数
- I1 与朴素参照逐 id 相同：alloc/alloc.go 的 `Allocate`（分组聚合注水）对 `NaiveAllocate`（逐任务重扫）；测试 TestNaiveEquivalence。
- I2 两条硬约束且至少一条达上界：alloc/alloc.go `Allocate` 逐段 ds=(C-U)/Σβ 取 min 推进；测试 TestHardConstraints。
- I3 主导份额最大最小公平：alloc/alloc.go 资源绑定后仅对其零需求的单资源组进入下一段抬升；测试 TestFairShares。
- I4 失败不留痕：api/api.go `New`/`Add` 全部校验通过后才改 map/聚合；测试 TestRejectedOpsLeaveNoTrace（五类互异哨兵另见 TestSentinelErrors）。
