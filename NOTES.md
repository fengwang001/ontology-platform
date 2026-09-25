# RTO 推导与不变量（baseRTO=10）

| # | 操作 | rto | deadline | 未确认段 | 本步超时 |
|---|---|---|---|---|---|
| 1 | Send(0,0) | 10 | 10 | {0} | 否 |
| 2 | Tick(10) | 20 | 30 | {0} | 是，重传 0 |
| 3 | Tick(20) | 20 | 30 | {0} | 否 |
| 4 | Tick(30) | 40 | 70 | {0} | 是，重传 0 |
| 5 | Ack(1,40) | 10 | 停 | {} | 否 |
| 6 | Send(1,50) | 10 | 60 | {1} | 否 |
| 7 | Tick(60) | 20 | 80 | {1} | 是，重传 1 |
| 8 | Ack(2,60) | 10 | 停 | {} | 否 |

(甲) 第 4 步后正确 rto=40、deadline=70。加法退避（rto+=10）：第 2 步 rto=20、dl=30；第 4 步 rto 错成 30、deadline 错成 60。
(乙) 先 Tick 后 Ack：now==deadline 命中左闭，段 1 被重传。顺序反过来先 Ack(2,60)：段 1 被移除、计时器停，再 Tick(60) 无未确认段，不触发、不重传。同刻下先到的 Ack 可避免一次本可避免的重传。
(丙) 第 6 步后正确 rto=10、deadline=60。若不重置：保留 rto=40，deadline 错成 90；于是 Tick(60) 不再触发，段 1 的重传被推迟到 90，且退避在 40 上继续叠加，恢复显著变慢。

不变量（代码保证位置 / 钉住的测试函数）：
I1 与朴素参照一致：rto.go `State.Probe`（只判 now>=deadline）+ rtx.go `Engine.Tick`（重传最早段）；api_test.go `TestNaiveReferenceRandom` 随机交错对拍，`SelfCheck` 核内置八步。
I2 RTO 翻倍/重置：rto.go `Probe` 翻倍、`ApplyAck` 重置 baseRTO 并清 backoff；`TestEightStepDerivation`。
I3 计时器指向最早未确认段：rto.go `Send`/`ApplyAck` 重算 earliest（有序切片 head），空集合即 disarm；`TestTimerPointsAtEarliest`。
I4 失败不留痕：rtx.go `Send`/`Ack` 先校验时钟/序号再改任何状态；`TestRejectedOpsLeaveNoTrace`。
非导出探测计数 `lastTickProbed`（rto.go，不经任何导出接口读取）：`TestProbeCountBounded` 白盒钉死 ≤1。
