# NOTES

## 八步推导（初始时钟 0；排队按到期序列出）

| 步 | 操作 | 操作后排队（id@fireAt） | 触发 |
|---|---|---|---|
| 1 | Schedule("a",5) | a@5 | — |
| 2 | Schedule("b",3) | b@3, a@5 | — |
| 3 | Schedule("c",5) | b@3, a@5, c@5 | — |
| 4 | Schedule("d",2) | d@2, b@3, a@5, c@5 | — |
| 5 | Cancel("b") | d@2, a@5, c@5（b@3 仍在堆中，已取消） | — |
| 6 | Schedule("b",4) | d@2, b@4, a@5, c@5 | — |
| 7 | Tick(5) | 空 | [d,b,a,c] |
| 8 | Tick(5) | 空 | []（不重复触发） |

- **(甲)** 若取消只维护「已取消 id 集合」按 id 判定：第 6 步新调度的 b@4 与旧 b@3 不区分世代，Tick(5) 弹出 b@4 时仍命中取消标记 → 输出 **[d,a,c]**：新任务 **b 被漏掉**（旧 b@3 该跳、新 b@4 该触发却被一起误杀）。
- **(乙)** 若堆键只比 fireAt 不比注册序：第 3 步后 a、c 同键，先后由堆内位置决定、**任意且不保证注册序**；第 7 步 Tick(5) 可能输出 **[d,b,c,a]**，即 a 与 c 被颠倒（正确为 [d,b,a,c]）。
- **(丙)** 若按注册序 FIFO 触发、忽略 fireAt：活跃条目注册序为 a(1),c(3),d(4),b(6)，Tick(5) 输出 **[a,c,d,b]**；fireAt 序列为 5,5,2,4，**违反不变量 2（同一次 Tick 内 fireAt 必须非降）**。

## 四条不变量：保证位置 / 钉住测试

1. 与朴素参照一致：sched 只经 dlq 堆序弹出、取消按条目世代标记（sched/sched.go Tick）→ `TestNaiveReference`
2. 触发序：dlq.less 按 `(fireAt, seq)` 比较（dlq/dlq.go）→ `TestFiringOrder`
3. 单调时钟：sched.Tick 先比对 lastNow 再改状态（sched/sched.go）→ `TestClockMonotonic`
4. 失败不留痕：api 全部校验先于任何状态修改（api/api.go）→ `TestRejectedNoTrace`

堆下沉复杂度由 dlq 非导出计数器 checks 见证（仅同包测试可读）→ `TestPopChecksSublinear`；并发由 api 互斥锁保证 → `TestConcurrentCancel`。
