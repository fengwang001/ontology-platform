# 实测结论

## 舱壁（bulkhead）

- 500 协程、N=8：历史峰值恰为 8，恒不超过额度（`TestPeakUnderConcurrency`）。
- N=1、Q=1 时第 3 个请求立即得到 `ErrFull`，注入时钟读数不变、
  `LastReject` 用注入时钟打戳（`TestQueueFullRejectsImmediately`）。
- 排队请求被上游取消后队列名额归还，后续请求可重新排队
  （`TestCancelReleasesQueueSlot`）。
- N=1 退化为串行：50 协程共享计数器最终值 50、峰值 1
  （`TestSerialWhenLimitOne`）。
- X 额度耗尽时 Y 的 100 次调用全部成功（`TestIsolationBetweenDownstreams`）。
- 成功/失败/超时/panic 四条路径各 1000 次后在途与排队均归零
  （`TestReleaseOnAllPaths`）；超时路径错误 `errors.Is` 命中
  `timeout.ErrTimeout`，panic 路径 `errors.As` 命中 `*timeout.PanicError`。
- 以上在 `go test -race` 下同样通过。

## 表①：熔断状态机迁移（待补）

## 表②：5 万次随机调用统计恒等式（待补）
