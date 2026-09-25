# DESIGN — 熔断与舱壁隔离的调用保护器

调用链（唯一组合点，demo 与测试共用同一形态）：

```
guard.call: breaker.Allow → bulkhead.Acquire → timeout.Do(fn) → breaker.Report + stat.Record
```

## 1. 口径推导：被熔断拒绝的调用不计入失败率

设失败率 `r = 失败数 / 真实调用数`。若把"被熔断拒绝"计入分子或分母：

- 打开后每次拒绝都使分子 +1（拒绝本身被当成失败），`r` 立即被拉满到 1；
- 冷却结束后进入半开，只要窗口里还残留这些"伪失败"，`r` 仍超阈值，一次探测即被打回打开；
- 于是打开 ⇒ 永远打开，熔断器退化为一次性开关。

结论：失败率的分子分母只能来自**真实触达下游的调用**（成功 / 失败 / 超时 / panic）。
被拒绝的调用进入两个独立计数器（`BreakerRejected` / `BulkheadRejected`），与失败率完全隔离。
推论：熔断打开后连续 1000 次被拒，`真实调用数` 与 `失败数` 都不得变化；半开后一次成功即可关闭。

## 2. 顺序论证：熔断在前，舱壁在后

两种序的对比：

- **熔断在前**：打开期间拒绝是 O(1) 的本地判断，不占用任何并发额度，不排队；
  下游已知故障时，舱壁额度完整保留给状态翻转后的探测与恢复流量。
- **舱壁在前**：拒绝也要先排队拿额度。下游故障期间，"注定要被熔断拒绝的请求"会占满
  N 个在途额度与 Q 个等待位，把舱壁挤兑成故障放大器；且每个拒绝都要付出排队延迟。

故规定 **熔断在前**。可观测推论：熔断打开期间舱壁在途占用恒为 0（demo 与测试均断言）。

## 3. 额度必须在所有路径上归还

真实调用有四条出口：成功、业务失败、超时、panic。任一路径漏还额度，额度单调泄漏，
最终可用额度归零、舱壁永久全拒（静默死锁）。实现上用 `defer release()` 在 Acquire
成功后立即挂上，四条路径共用同一归还点；panic 在 `timeout.Do` 内 recover 成普通错误，
不会绕过 defer，也不会击穿包装器。测试对四条路径各跑 1000 次，断言可用额度回满。

## 4. 不可重试错误是否计入熔断：不计入

定义：`classify` 把真实调用的结果分为 可重试 / 不可重试 / 超时 三类。

- **可重试**（连接失败、503 等）与**超时**：下游可能处于故障态，计入熔断失败。
- **不可重试**（参数错误、404 等业务拒绝）：下游正确地处理并拒绝了请求，这恰恰是
  "下游健康"的证据。若计入失败率，一个发烂请求的客户端就能熔断所有正常客户端。
  故不可重试错误：不计入熔断失败、重置连续失败计数，但仍计入 `stat` 的失败数
  （统计口径与熔断口径是两件事，等式只约束统计内部自洽）。
- **panic**：下游状态未知，保守计为可重试失败（宁可误开，不可漏开）。

## 5. 熔断状态机语义

- 关闭 → 打开：`连续失败 ≥ ConsecutiveFailures`，或（窗口样本 ≥ MinSamples 且失败率 > FailureRate）。
  窗口 = 进入关闭态以来的成功/失败计数，状态迁移时清零。
- 打开 → 半开：`now - openedAt ≥ cooldown`（注入时钟）。`now < openedAt` 即时钟回拨：
  返回 `ErrClockRollback`，状态不变，不得提前半开。
- 半开 → 关闭：半开期成功探测数达到 `HalfOpenProbes`。
- 半开 → 打开：任一失败立即打开，`cooldown = min(cooldown*2, MaxCooldown)`（加倍只发生在
  半开→打开；关闭→打开不加倍；回到关闭时冷却复位为基准值）。
- 半开并发：同时在途探测 ≤ `HalfOpenProbes`，超出立即拒绝（`ErrOpen`）。
- 所有迁移在互斥锁内完成并计数，并发触发失败时迁移只发生一次、冷却只加倍一次。

## 6. 舱壁语义

- 在途 ≤ N（channel 容量保证），非导出峰值计数器记录历史峰值（`Peak()` 只读暴露）。
- 在途满且等待数 < Q 时排队等待；第 N+Q+1 个请求**立即**以 `ErrFull` 拒绝，不推进任何时钟。
- 等待中被 ctx 取消：离开等待队列，不持有也不泄漏名额。
- 每个下游一个 `Bulkhead` 实例，额度天然隔离。

## 7. 统计口径（stat）

计数器：`Total, BreakerRejected, BulkheadRejected, Success, Failure, Failures[3]（按类别）`，
`Real = Total - BreakerRejected - BulkheadRejected`。任意操作序列后恒成立：

1. `Total = Success + Failure + BreakerRejected + BulkheadRejected`
2. `Real = Total - BreakerRejected - BulkheadRejected`
3. `Failures[可重试]+Failures[不可重试]+Failures[超时] = Failure`

注：Acquire 阶段的一切失败（含等待中被取消）统一计入 `BulkheadRejected`，
保证每个请求恰好落进一个桶，等式 1 才恒成立。

## 8. 四类可判定错误（均可用 errors.Is 区分）

`breaker.ErrOpen`、`breaker.ErrClockRollback`、`bulkhead.ErrFull`、`timeout.ErrTimeout`。
panic 被包装为 `timeout.PanicError`（可用 errors.As 取出）。
