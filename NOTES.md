# ontology-647 NOTES

## 八步推导（初始时钟 0；每格为操作后活跃任务，按 (fireAt,注册序)）
1. Schedule(a,5)  → 队列 a@5；触发 无
2. Schedule(b,3)  → 队列 b@3 a@5
3. Schedule(c,5)  → 队列 b@3 a@5 c@5
4. Schedule(d,2)  → 队列 d@2 b@3 a@5 c@5
5. Cancel(b)      → 队列 d@2 a@5 c@5（b@3 留堆作墓碑）
6. Schedule(b,4)  → 队列 d@2 b@4 a@5 c@5
7. Tick(5)        → 队列空；触发 [d,b,a,c]
8. Tick(5)        → 队列空；触发 []（同时刻不重复触发）

甲：取消若按「已取消 id 集合」判定，第 6 步重排的 b@4 仍命中该集合 → Tick(5) 漏掉 b，错成 [d,a,c]（旧 b@3 墓碑本就该跳，新 b 被误杀）。
乙：堆键只比 fireAt 时，第 3 步后 a、c 同键先后由堆内部位置决定、无保证；Tick(5) 可能错成 [d,b,c,a]，a/c 被颠倒。
丙：纯 FIFO 按注册序触发（a,c,d,b）忽略 fireAt → Tick(5) 输出 [a,c,d,b]，违反 I2（fireAt 非降：5,5,2,4）。

## 第二节四条不变量：保证位置 / 钉测测试
I1 朴素参照一致：sched/sched.go 的 Tick 经 dlq.PopDue 取 (fireAt,seq) 到期前缀，世代以独立 seq 判定，不按 id 记取消 — TestNaiveReference（api/api_test.go）
I2 触发序：dlq/dlq.go 的 h.less 先比 fireAt 再比 seq，PopDue 依堆序逐个弹出 — TestPopDueOrder（dlq/dlq_test.go）
I3 单调时钟：sched/sched.go 的 Tick 入口先判 now<lastTick→ErrClockRewind，拒绝发生在出堆之前 — TestMonotonicClock（api/api_test.go）
I4 失败不留痕：sched 的 Schedule/Cancel/Tick 全部先校验通过后才改 heap/map/seq/history — TestRejectedOpsNoTrace（api/api_test.go）

复杂度：dlq.Heap.inspected 为非导出字段，仅包内 TestPopInspectCounter 读其数值；对外只给 SelfCheck 内的布尔判定，数值不跨包边界。
