# Max-Min Fair Share 推导与不变量

## 分步表（C=30，A=6 B=12 C=18 D=30，sum=66>30，受约束）

| 步 | 事件 | 剩余容量 | 剩余任务数 | fair | 本步分配 |
|---|---|---|---|---|---|
| 1 | 最小 demand 6 ≤ 15/2，A 全额满足 | 30→24 | 4→3 | 30/4=15/2 | A=6 |
| 2 | 最小 demand 12 > 8，达最终水位 | 24→0 | 3→0 | 24/3=8 | B=8, C=8, D=8 |

最终：A=6 B=8 C=8 D=8，合计恰为 30。

- (甲) 按比例 a_i=30·d_i/66=5d_i/11：A=30/11、B=60/11、C=90/11、D=150/11。C(90/11>8)、D(150/11>8) 超过其最大最小份额 8；A(30/11<6)、B(60/11<8) 被压低。
- (乙) 只算一次 fair=30/4=15/2、按 min(demand,15/2) 不再重归一化：A=6，B=C=D=15/2，合计 57/2，闲置 30−57/2=3/2；B、C、D 各被漏 1/2（应得 8）。
- (丙) C=100≥66 不受约束：A=6 B=12 C=18 D=30，闲置 34。若错误地「总填到相等水位」100/4=25，D 被错扣成 25（少 5），且 A/B/C 被给到 25 超过各自 demand。

## 四条不变量

1. 与朴素参照一致：`alloc.Engine.Allocate` 与 `alloc.Naive` 逐 id 精确相等；测试 `TestAgainstNaive`。
2. 最大最小：`alloc/alloc.go` 水位填充——仅当最小 demand ≤ fair 才全额，否则剩余全员同得 fair；测试 `TestMaxMinProperty`。
3. 守恒：`api.SelfCheck` 用 `mf.Frac` 精确相加核验 sum=min(C,sum demand)；测试 `TestConservation`。
4. 失败不留痕：`api.New`/`api.Add` 先校验后落地，四个互不相同哨兵错误；测试 `TestFaultInjection`。
