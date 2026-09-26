# NOTES

## 八步推导（capacity=3, interval=5）

| 步 | Submit | 排水移除 | 判定前系统内 | 结果 | 新出发时间 | 判定后系统内 |
|---|---|---|---|---|---|---|
| 1 | (0)  | 0 | 0 | 放行 | 5  | 1 |
| 2 | (5)  | 1（移走 dep=5） | 0 | 放行 | 10 | 1 |
| 3 | (6)  | 0（10>6） | 1 | 放行 | 15 | 2 |
| 4 | (10) | 1（移走 dep=10） | 1 | 放行 | 20 | 2 |
| 5 | (15) | 1（移走 dep=15） | 1 | 放行 | 25 | 2 |
| 6 | (15) | 0（20>15） | 2 | 放行 | 30 | 3 |
| 7 | (15) | 0 | 3 | 拒绝（满） | 无 | 3 |
| 8 | (21) | 1（移走 dep=20） | 2 | 放行 | 35 | 3 |

- (甲) 第 7 步不判满则放行，接在 dep=30 之后：出发时间 = **35**。
- (乙) 第 3 步正确出发时间 = **15**（接在前一项 dep=10 后）；若直接写 t+interval 会错成 **11**。
- (丙) 第 2 步 dep=5 ≤ t=5 必须移走，系统内剩 **0** 项；若排水写成 `dep < t`，5 不小于 5 被误留，系统内错成 **1** 项。

## 不变量：代码保证位置 / 钉住的测试函数

1. 与朴素参照一致：`sched.Submit`（先 `lb.Drain` 弹出 ≤t，再按 `InSystem()==capacity` 判满，再追加）——`TestNaiveReference`。
2. 平滑性：`lb.Admit` 中空桶 dep=t+interval、否则 dep=队尾+interval，出发时间严格递增 ——`TestSmoothness`。
3. 容量不越界：`lb.Admit` 满则返回 full 不入环；`sched.Submit` 据此拒绝 ——`TestCapacityBound`。
4. 失败不留痕：`sched.Submit` 全部校验在任何写操作之前，拒绝路径不触队列 ——`TestRejectionLeavesStateUnchanged`。

附：排水只探测队头（非导出 `drainChecks`）——`TestDrainChecksBounded`；并发 ——`TestConcurrentSubmit`。
