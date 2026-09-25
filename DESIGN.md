# 熔断与舱壁保护器 — 设计推导

模块 `ontology`，仅标准库，状态驻留进程内存，时钟经 `Clock` 接口注入。
包：`classify` / `bulkhead` / `timeout` / `breaker` / `stat`，`cmd/demo` 串联。

## 1. 调用链顺序：熔断在前，舱壁在后

顺序定为 **breaker → bulkhead → timeout → 真实调用**。
- 熔断已知下游故障时应立即拒绝；若舱壁在前，连"明知必败"的拒绝也要先排队占名额，
  会挤占可能在冷却结束后发出的探测请求。
- 推论：熔断打开期间被拒的请求从未触及舱壁，故舱壁在途占用恒为 0。
- 被熔断拒绝与被舱壁拒绝是两类可判定错误（`errors.Is` 可区分），不互相混算。

## 2. 熔断计数口径：拒绝不计失败率

记真实调用结果为成功 S 或失败 F，熔断拒绝 Rb、舱壁拒绝 Rq。
- 失败率分母只含**真实调用**：`rate = F / (S + F)`。Rb 进分母会自我强化：
  打开后每次拒绝都记失败 → 失败率恒为 100% → 永不恢复。
- 故统计恒等式：
  - `总请求 = S + F + Rb + Rq`（每个请求恰落入一个终态桶）
  - `真实调用 = 总请求 - Rb - Rq = S + F`
  - `F = F_retry + F_fatal + F_timeout`（失败按类别细分，和不变）
- 半开探测属于真实调用；探测成功/失败正常计入。

## 3. 不可重试错误是否计入熔断

**不计入**。熔断保护的是"稍后重试可能恢复"的故障；不可重试错误（参数非法、4xx 语义）
重发结果不变，计入只会因与下游健康无关的原因错误打开熔断。
`classify` 把错误分为 Retryable / Fatal / Timeout；breaker 仅对 Retryable 与 Timeout
计失败；但 `stat` 的失败桶对三类全部计数（统计口径与熔断口径分离）。
panic 被包装器捕获，按 Retryable 失败处理（下游进程崩溃属可恢复故障），并保证不击穿。

## 4. 舱壁语义

- 容量 N（在途）+ 队列 Q（等待），第 N+Q+1 个请求立即返回队列满错误，不等待、不推进时钟。
- 内部一个 mu + 等待者 channel 列表；发放名额在临界区内投递 channel，
  占用数恒等于在途真实调用，峰值 `peak` 非导出计数，同锁更新。
- 名额归还对成功/失败/超时/panic 四条路径必经（`defer release()`，`sync.Once` 幂等）。
  归还时若有等待者，名额直接转交，占用不回落；否则占用减一。
- 等待者被 `ctx` 取消时从队列摘除：其后若被发放，必须把名额让给下一等待者而非泄漏。
- 每个下游一个独立 Bulkhead 实例，故 X 耗尽不影响 Y。

## 5. timeout

`Do(ctx,d,fn)`：派生带截止 ctx，fn 在 goroutine 中执行；截止先到返回
ErrTimeout（可被 errors.Is），fn 结果/ panic 经 buffered channel 回收，
panic 不在任何路径上击穿（接收后 re-fail 为可重试错误返回，而非重新 panic）。

## 6. 熔断状态机

- Closed：连续失败 ≥ Consecutive，或 `真实调用样本 ≥ MinSamples 且失败率 > Rate` → Open。
- Open：注入时钟 `now ≥ openedAt + cooldown` 才允许转 Half-open；时钟回拨（now 变小）
  返回 ErrClockBackwards，状态不变、冷却不重置。
- Half-open：至多 Probe 个探测通过（在途+已放行计数），其余直接拒绝；
  全部探测成功 → Closed（计数清零）；任一可计失败 → Open，冷却 = min(冷却×2, 上限)。
- Fatal 失败在 Half-open 也判失败（探测已发出，结果坏就应重新打开），仅 Closed 期不计数。
- 迁移在锁内完成并由 transitions 计数；并发失败只迁移一次，故加倍也只发生一次。

## 7. 校验

N/Q/阈值/冷却/探测数 ≤ 0 一律构造报错。四类错误：
ErrRetryable、ErrFatal、ErrTimeout、ErrRejected（下挂 ErrBreakerOpen/ErrBulkheadFull）
及 ErrClockBackwards，均以哨兵错误 + `errors.Is` 判定。
