# 漏桶推导与不变量

## 八步推导（capacity=3, interval=5）

| # | t | 排水移除 | 判定前系统内 | 结果 | 出发时间 | 判定后系统内 |
|---|---|----------|--------------|------|----------|--------------|
| 1 | 0  | 0 | 0 | 放行 | 5  | 1 |
| 2 | 5  | 1 | 0 | 放行 | 10 | 1 |
| 3 | 6  | 0 | 1 | 放行 | 15 | 2 |
| 4 | 10 | 1 | 1 | 放行 | 20 | 2 |
| 5 | 15 | 1 | 1 | 放行 | 25 | 2 |
| 6 | 15 | 0 | 2 | 放行 | 30 | 3 |
| 7 | 15 | 0 | 3 | 拒绝 | 无 | 3 |
| 8 | 21 | 1 | 2 | 放行 | 35 | 3 |

- (甲) 第7步 Submit(15)：不判满则放行，出发时间 = 上一项 30 + 5 = **35**。
- (乙) 第3步 Submit(6)：正确 dep = 10 + 5 = **15**；若直接写 t+interval 会错成 6 + 5 = **11**。
- (丙) 第2步 Submit(5)：正确排水（≤t）把 dep=5 的项移走，系统内剩 **0** 项；若写成 <t 会误留该项，错成 **1** 项（容量被虚占，后续判定全错）。

## 四条不变量落点

1. 朴素参照一致：`lb.Drain` 队头排水 + `lb.Admit` 判满接尾（lb/lb.go），测试 `TestNaiveConsistency`、`TestEightStep`。
2. 平滑性：`lb.Admit` 非空时 dep = last + interval（lb/lb.go），测试 `TestNaiveConsistency`（逐步断言间隔≥interval）。
3. 容量不越界：`lb.Admit` 系统内项数==capacity 即拒（lb/lb.go），测试 `TestNaiveConsistency`、`TestConcurrentSubmit`。
4. 失败不留痕：`api.New` 先校验再建桶；`sched.Submit` 校验失败直接返回、不触状态（api/api.go、sched/sched.go），测试 `TestFailureAtomicity`。
