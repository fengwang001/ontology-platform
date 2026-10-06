# 营业时段与临时歇业管理 API

包 `shophours`（位于仓库根包）核心类型为 `System`，构造：

```go
s, err := shophours.NewSystem(shophours.Config{
    BaseTime:                1_700_000_000, // 周划分基准时刻（整数秒）
    WeekSec:                 7 * 24 * 3600,
    Intervals:               []shophours.Interval{{Start: 9 * 3600, End: 22 * 3600}},
    PreCloseLead:            15 * 60,
    MaxClosureDuration:      3 * 3600,
    MinClosureGap:           24 * 3600,
    MaxReservationAheadDays: 7,
})
```

区间 `Interval{Start,End}` 为周内左闭右开秒偏移：`Start<End` 为普通区间，
`Start>End` 表示跨周尾回绕；同表区间不得重叠或相接，`Start==End` 非法。

方法（所有 `now` 必须非递减，否则返回 `ErrClockRollback`）：
- `SubmitTable(now, ivs)`：下一个周边界生效，重复提交覆盖待生效表；停业期间也允许。
- `StartTemporaryClosure(now, start, duration, cancelPending)`：发起歇业；
  `cancelPending=true` 连带以商家责任取消已接未开工即时单。
- `EndTemporaryClosure(now)`：提前结束，实际结束时刻作为下次间隔起算点。
- `ForceSuspend(now, start)` / `LiftForceSuspension(now)`：平台强制停业（无截止）/解除。
- `AcceptInstant(now, prepDuration)` / `AcceptReservation(now, targetAt, prepDuration)`：
  准入判定，成功返回订单 ID。
- `StartOrder(now, id)` / `CompleteOrder(now, id)`：开工、完成报告。
- `GetOrder(id)`：查询订单快照（状态、承诺取货时刻、取消责任方/原因/时刻）。

错误均为 `*BizError`，用其 `Code` 字段（`ErrInvalidParam` … `ErrReservationTooFar`）
程序化区分；每个操作只返回拒绝次序中第一个命中的错误，被拒绝不产生任何副作用。

并发：方法可并发调用，内部等价串行；重放相同操作序列（相同时刻参数）结果一致。
