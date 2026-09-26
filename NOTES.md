# Sliding Window Rate Limiter — Notes

## 1. 八步推导 (limit=3, window=10)

| # | Allow | 窗口 [t-10,t) | 窗口内已放行 | 判定 | 步后已放行集合 |
|---|---|---|---|---|---|
| 1 | 0 | [-10,0) | - | 放行 (k=0) | {0} |
| 2 | 2 | [-8,2) | {0} | 放行 (k=1) | {0,2} |
| 3 | 5 | [-5,5) | {0,2} | 放行 (k=2) | {0,2,5} |
| 4 | 7 | [-3,7) | {0,2,5} | 拒绝 (k=3) | {0,2,5} |
| 5 | 10 | [0,10) | {0,2,5} | 拒绝 (k=3) | {0,2,5} |
| 6 | 11 | [1,11) | {2,5} | 放行 (k=2) | {2,5,11} |
| 7 | 12 | [2,12) | {2,5,11} | 拒绝 (k=3) | {2,5,11} |
| 8 | 20 | [10,20) | {11} | 放行 (k=1) | {11,20} |

(甲) Allow(10) 正确为**拒绝**；退化成固定窗口则对齐到桶 [10,20)，桶内被数成 **0 个**，把 0,2,5 算进上一桶，**错判放行**。
(乙) 左边界写成开区间 (ts>2) 则 ts=2 被排除，k=2，Allow(12) **错判放行**，集合错成 {2,5,11,12}。
(丙) 判定写成 k≤limit 则 3≤3 成立，Allow(7) **错判放行**，集合错成 {0,2,5,7}。

## 2. 不变量：保证位置 / 钉住测试

- I1 朴素参照一致：`lim.Limiter.Allow`（lim/lim.go）先 EvictExpired 再按 k<limit 判定；钉于 `Test_ReferenceEquivalence`（api/api_test.go）。
- I2 过期项必弹出：`win.Queue.EvictExpired`（win/win.go）用队头指针循环弹出 ts<t-window；钉于 `Test_EvictExpiredQueue`（win/win_test.go）。
- I3 单调性：`lim.Limiter.Allow`（lim/lim.go）改状态前校验 t<lastT 返回 ErrClockRollback；钉于 `Test_ClockMonotonic`（api/api_test.go）。
- I4 失败不留痕：三类错误分支全部在任何状态修改之前返回（api.New、lim/lim.go）；钉于 `Test_FailureLeavesNoTrace`（api/api_test.go）。
