# 带优先级抢占的变更事件处理器 — 推导与不变量

## 八步分步表（E1..E4 即 ID 1..4；pending 为各档原始队列；suspend 自底向上）

| # | 操作 | pending | current | suspend | done |
|---|------|---------|---------|---------|------|
| 1 | Submit(E1,1) | {1:[E1]} | nil | [] | [] |
| 2 | Submit(E2,1) | {1:[E1,E2]} | nil | [] | [] |
| 3 | Process() | {1:[E1,E2]} | {1,1} | [] | [E1] |
| 4 | Submit(E3,3) | {1:[E1,E2],3:[E3]} | {3,0} | [{1,1}] | [E1] |
| 5 | Submit(E4,5) | {1:[E1,E2],3:[E3],5:[E4]} | {5,0} | [{1,1},{3,0}] | [E1] |
| 6 | Process() | {1:[E1,E2],3:[E3]} | {3,0} | [{1,1}] | [E1,E4] |
| 7 | Process() | {1:[E1,E2]} | {1,1} | [] | [E1,E4,E3] |
| 8 | Process() | {} | nil | [] | [E1,E4,E3,E2] |

(甲) 第 4 步保存的现场是 {prio=1, pos=1}。若抢占时不保存现场、恢复时游标重置为 0，第 8 步会从下标 0 重新处理：E1 被重复，done 错成 [E1,E4,E3,E1,E2]。
(乙) 第 6 步处理完 E4 后恢复栈顶 {3,0}（E3 批次）。若 suspend 按 FIFO 先恢复最早被抢占的 {1,1}：第 7 步处理 E2、第 8 步才处理 E3，done 错成 [E1,E4,E2,E3]，E2 被提前到 E3 之前。
(丙) 正确 done 中 E1 先于 E2。同优先级若用栈（LIFO）：E2 先出，done 错成 [E2,E4,E3,E1]；若按 ID 排序：相对顺序由 ID 大小而非到达先后决定，ID 与到达顺序不一致时（如 E2.ID<E1.ID）E2 会排在 E1 前。

## 四条不变量：保证位置 → 钉住它的测试

1. 朴素一致：sched.Process 仅空闲时经 qs.Highest 选最高非空档、档内 pos 严格递增、耗尽恢复栈顶，与「按同一组给定规则逐步模拟的在线参照」逐步一致 → sched.TestNaiveConsistency 逐 op 对比（含 8 步脚本精确 done=[1,4,3,2]）。
2. 不重复不丢失：sched.Submit 的 seen 去重；批次耗尽才 qs.Drop；抢占现场保存 pos → sched.TestNaiveConsistency 排空后多重集相等断言。
3. 同优先级 FIFO：q.Add 尾部追加 + sched 按 pos 递增消费 → sched.TestNaiveConsistency 的同档到达序断言与 api_test.TestEightOpScript。
4. 失败不留痕：sched.Submit/Process 全部校验通过后才改动任何字段 → api_test.TestFaults 的快照前后一致断言。

复杂度：sched.lastChecked（非导出）记录最近一次 Process 选档检查的分档数，sched.TestPickChecksBucketsNotEvents 钉住其 ≤P、不随 n 增长。
