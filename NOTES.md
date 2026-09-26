# NOTES

## 一、八步推导（limit=3, window=10，窗口左闭右开 [t-10,t)，每步先弹过期再计数）
| 步 | 判定窗口 | 窗口内已放行时间戳 | 判定 | 步后已放行集合 |
|---|---|---|---|---|
| 1 Allow(0) | [-10,0) | 无 (k=0) | 放行 | {0} |
| 2 Allow(2) | [-8,2) | {0} (k=1) | 放行 | {0,2} |
| 3 Allow(5) | [-5,5) | {0,2} (k=2) | 放行 | {0,2,5} |
| 4 Allow(7) | [-3,7) | {0,2,5} (k=3) | 拒绝 | {0,2,5} |
| 5 Allow(10) | [0,10) | {0,2,5}（含边界 ts=0, k=3） | 拒绝 | {0,2,5} |
| 6 Allow(11) | [1,11) | {2,5}（ts=0 因 <1 已弹出, k=2） | 放行 | {2,5,11} |
| 7 Allow(12) | [2,12) | {2,5,11}（ts=2 恰在左边界, 计入, k=3） | 拒绝 | {2,5,11} |
| 8 Allow(20) | [10,20) | {11}（2、5 因 <10 已弹出, k=1） | 放行 | {11,20} |

（甲）Allow(10) 正确为**拒绝**（k=3）。退化固定窗口按 [⌊t/10⌋·10,…) 对齐：0/2/5 全属上一 bucket [0,10)，此刻 bucket [10,20) 内被数成 **0 个**，错判为**放行**（边界突发）。
（乙）左边界写成开区间 ts>2：ts=2 被排除，只剩 {5,11}，k=2<3，错判为**放行**。
（丙）判定写成 k≤limit：k=3≤3 成立，错判为**放行**（放进第 4 个请求，突破 limit=3）。
注：同一时间戳 t 的先前放行也计入占用（计数右端闭到 t），否则第六节「同 t 并发放行数 ≤ limit」不可能成立；严格递增序列上与 [t-w,t) 朴素参照逐拍等价。

## 二、四条不变量：代码保证位置 / 钉住的测试函数
1. 与朴素参照一致：`lim.Limiter.Allow` 先 `win.Queue.EvictExpired` 再 `InWindow` 计数判定（lim/limiter.go）；钉住：TestAllowMatchesNaiveReference、TestEightStepDecisions（api 包）。
2. 已放行集合恰为窗口内：`win.Queue.EvictExpired` 只从队头弹出 ts < t-window（== 边界保留）（win/window.go）；钉住：TestQueueSemantics（win 白盒）、TestSelfCheck。
3. 单调性：`Allow` 在任何写操作之前校验 t<0 与 t<lastT，违例直接返回哨兵错误（lim/limiter.go）；钉住：TestClockMonotonicity。
4. 失败不留痕：`lim.New` 非法参数直接返回 nil；`Allow` 三条非法路径全部先于 lastT/队列/accepted 的写入（lim/limiter.go）；钉住：TestRejectedCallsLeaveNoTrace、TestSentinelErrorsDistinct。
