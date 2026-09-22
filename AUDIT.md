# 不变量与复杂度审计

## 不变量逐条核对

**1. 不早触发、不漏触发**
判定 `now >= deadline`（`>=` 的推导见 DESIGN.md 推导一）。
保证位置：`scheduler/advance.go` 的 `step` 每 tick 推进游标并收集到期者，
`scheduler/scheduler.go` 的 `insertLocked` 按剩余 tick 精确定位层与槽，
`cascade.Layout.Level/Slot` 保证 `deadline` 与槽位的换算闭合。
钉住测试：`scheduler.TestFireTiming`（d=0/1/5/63/64/100/1000，推进 d-1 不触发、
到达 d 恰好触发一次、之后再推进不重复触发）。

**2. Advance(100) 与一百次 Advance(1) 序列相同**
保证位置：`Advance` 内部就是 n 个 `step` 的循环（`scheduler/advance.go`），
单 tick 处理是状态的纯函数；降级只发生在游标落到槽时（惰性，DESIGN.md 推导三）。
钉住测试：`scheduler.TestAdvanceEquivalence`（13 个跨层延迟，两种推进方式
触发序列逐位相等）。

**3. 同 tick 顺序确定且与层级无关**
保证位置：每次注册分配单调 `seq`（`nextSeqLocked`），`collectLocked` 汇总
due 队列与层 0 当前槽后按 `seq` 排序（`scheduler/advance.go`）。
钉住测试：`scheduler.TestSameTickOrder`（混合延迟的触发顺序 = 到期时刻分组、
组内注册先后；同一输入重复执行结果逐位相同）。

**4. 取消即不触发（含已取出待触发时被取消）**
保证位置：`fireAll` 在每次回调前持锁复查句柄状态，非 Pending 直接跳过
（`scheduler/advance.go`）；回调不持锁执行，`Cancel` 可在回调内进入。
钉住测试：`scheduler.TestCancelInCallback`（A 的回调取消同 tick 的 B，
B 不触发且 Cancel 返回 nil）。

**5. 句柄幂等，三类结果可判定且互不相同**
保证位置：`stateErrLocked` 把句柄状态映射为 `ErrAlreadyCancelled` /
`ErrAlreadyFired` / `ErrUnknownTimer`，成功路径返回 nil
（`scheduler/scheduler.go`）；状态机本身在 `timer.Timer` 的
`Cancel/Fire/Reset` 中幂等。
钉住测试：`scheduler.TestHandleIdempotency`（重复 Cancel、Cancel 已触发、
Reset 已取消、Reset 已触发、Reset 待触发、空句柄与外来句柄）；
状态机层另有 `timer.TestStateMachine` 的 9 组转移表。

## 复杂度约束

计数器为非导出字段 `touchedSlots` / `touchedTimers`（`scheduler/scheduler.go`），
每次 `Advance` 开始时清零，只统计本次推进触碰的槽数与定时器数，不进公开接口。
实测（`scheduler.TestTouchIndependentOfN`，定时器全部远在未来，`Advance(1)`）：

| N      | touchedSlots | touchedTimers |
|--------|--------------|---------------|
| 1000   | 1            | 0             |
| 100000 | 1            | 0             |

两档之间增长为 0，远小于 100 倍；触碰数只与推进的 tick 数和到期/降级元素数有关。

## 故障注入与并发

- 参数与上限错误全部先校验后动状态（`Add/Reset/Advance` 入口），拒绝后调度器
  可继续工作：`scheduler.TestParamErrors`、`scheduler.TestLimitsAndRecovery`、
  `scheduler.TestConfigValidation`。
- 并发：`TestConcurrent` 中 8 个 goroutine 反复 Add/Cancel、1 个 goroutine
  反复 Advance，结束后 `Check` 为 nil、取消集与触发集不相交、
  触发数+取消数=注册数；`go test -race` 干净。
- 混合操作后游标与计数自洽：`scheduler.TestCheckAfterMixedOps`
  （含非零起始时刻 start=1000/123456789）。
