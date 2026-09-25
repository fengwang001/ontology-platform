# NOTES

## 第三节推导：interval=10，耗时 3,15,2,4

| 次数 | 模式 | 开始 | 结束 | rate 跳过 |
|---|---|---|---|---|
| 1 | rate | 0 | 3 | 否 |
| 2 | rate | 10 | 25 | 是，跳过 20 |
| 3 | rate | 30 | 32 | 否 |
| 4 | rate | 40 | 44 | 否 |
| 1 | delay | 0 | 3 | — |
| 2 | delay | 13 | 28 | — |
| 3 | delay | 38 | 40 | — |
| 4 | delay | 50 | 54 | — |

- (甲) rate 错成 delay：开始错成 0,13,38,50（应为 0,10,30,40），锚点丢失、向后漂移。
- (乙) delay 错成 rate（锚定上次预定触发+interval）：开始错成 0,10,20,30（应为 0,13,38,50）；且第 3 次 20 开始早于第 2 次结束 25，发生重叠。
- (丙) rate 下耗时 25（如 10 开始 35 结束）覆盖 20、30；下一次应从 40（第一个 ≥35 的网格点）开始。只跳一个被覆盖时刻会错从 30 开始，30 < 35，与尚未结束的上次执行重叠。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`period.Task.Run` 推进逻辑 = 参照语义；测试 `TestMatchesNaiveReference`（随机序列对拍）。
2. rate 不漂移：`fire.NextRate` 用除法取 interval 整数倍；测试 `TestRateNoDrift`。
3. delay 漂移：`fire.NextDelay` = 上次结束+interval；测试 `TestDelayDrift`。
4. 失败不留痕：`api.Scheduler.AddTask/Run` 先校验后变更；测试 `TestRejectionLeavesNoTrace`。
另：检查数不随 m 增长由 `fire.NextRate` 一次除法保证，测试 `fire.TestCheckedIsConstant`；并发由 `api.Scheduler` 的互斥锁保证，测试 `TestConcurrentRun`；`SelfCheck` 由 `TestSelfCheck` 钉住。
