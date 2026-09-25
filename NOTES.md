# PCP 推导与不变量落实

## 八步分步表（Rx 天花板=max(5,1)=5，Ry 天花板=max(3,1)=3）

| # | 操作 | 结果 | 操作后系统天花板 | 操作后有效优先级 |
|---|------|------|------|------|
| 1 | Acquire(T3,Rx) | granted（1>0） | 5 | T3=max(1,5)=5 |
| 2 | Acquire(T2,Ry) | blocked（3≯5） | 5 | T2=3 |
| 3 | Acquire(T1,Rx) | blocked（5≯5） | 5 | T1=5 |
| 4 | Release(T3,Rx) | ok | 0 | T3=1 |
| 5 | Acquire(T1,Rx) | granted（5>0） | 5 | T1=max(5,5)=5 |
| 6 | Acquire(T2,Ry) | blocked（3≯5） | 5 | T2=3 |
| 7 | Release(T1,Rx) | ok | 0 | T1=5 |
| 8 | Acquire(T2,Ry) | granted（3>0） | 3 | T2=max(3,3)=3 |

- (甲) min 错算：Rx 天花板=1、Ry 天花板=1。第 2 步系统天花板=1，3>1 → 错成 granted：T2 在 T3 持 Rx 期间拿到 Ry；T3 有效优先级仅被抬到 1，T2(3) 抢占 T3，T1 等 Rx 变成间接等 T2 —— 天花板失效，优先级反转重现。
- (乙) `>=` 错写：第 3 步 5>=5 → 错成 granted，Rx 同时被 T3 与 T1 持有，违反不变量 2（同一资源至多一个持有者，互斥被破坏）。
- (丙) 第 1 步后 T3 有效优先级应为 5。若不抬升（保持 1），最高有效优先级先运行的调度下 T2(3) 插到 T3(1) 前面，T1(5) 等 T3 释放 Rx 即间接等待 T2 —— 经典的无界优先级反转（unbounded priority inversion）。

## 四条不变量在代码中的落实

1. 与朴素参照一致：`lock.go` 的 `Acquire`/`sysCeilingLocked` 用增量多集（ceilCount/maxCeil）求系统天花板；`lock_test.go:TestNaiveConsistency` 随机操作下与全扫描朴素实现逐步比对。
2. 互斥：`lock.go` 中 `holder[res]` 单槽位，授予前校验占用、授予与天花板更新同临界区；`api_test.go:TestMutex` 钉住。
3. 天花板正确：`ceil/ceil.go` 的 `Use` 取 max 维护天花板；`lock.go` 的 `EffectivePriority` 取 max(基础, 持有天花板)；`api_test.go:TestCeilingAndBoost` 钉住。
4. 失败不留痕：`lock.go` 所有校验先于任何写，哨兵错误整体返回；`api_test.go:TestFaultInjection` 钉住（含拒后状态不变、可继续使用）。
