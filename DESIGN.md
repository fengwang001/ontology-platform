# 熔断与舱壁调用保护器 — 设计推导

## 1. 分层与顺序：熔断在前，舱壁在后

请求判定顺序为 `breaker.Allow -> bulkhead.Acquire -> timeout.Do(真实调用)`。

- 熔断已知下游故障，应在 O(1) 内立即拒绝；若先走舱壁，拒绝请求也要占用等待名额/排队，
  会把有限的并发额度与队列浪费在"注定不执行"的请求上，真实探测反而可能拿不到额度。
- 因此熔断打开期间，舱壁计数器恒为 0（测试断言）。
- 统计口径上一次请求分两层拒绝原因：先判熔断，再判舱壁，互斥。

## 2. 熔断计数只算真实调用（核心口径）

设熔断打开后 R 次请求被拒。若把拒绝计入失败：
`失败率 = (F+R)/(S+R)`，R→∞ 时失败率→1，冷却到期后探测永远无法在"高失败率"下被放行，
形成自锁，半开一次成功也回不到关闭。

正确口径：**熔断器只对真正执行过的调用记账**；被熔断拒绝、被舱壁拒绝都不进熔断器样本。
于是 1000 次拒绝后内部失败率不变；半开期一次成功即可满足"探测全部成功"回到关闭。
拒绝只计入 stat 的拒绝对外指标，不反馈给熔断器。

## 3. 不可重试错误是否计入熔断

不计入。不可重试错误（如参数非法 4xx 语义）是调用方确定性错误，与下游健康无关：
对它熔断会让一个坏调用拖垮所有正常调用。故 Class=Permanent 时不增加连续失败数、
不进失败率样本；Timeout 与 Retryable（含 panic 转化）计入失败。成功清零连续失败。

## 4. 舱壁语义

- 容量 N：计数器 in-flight ≤ N，内部记录历史峰值 peak（非导出）。
- 队列上限 Q：N 个在途 + Q 个等待之外的第 N+Q+1 个请求立即返回 ErrBulkheadRejected，
  不等待（用注入时钟断言 Now 未推进）。
- 等待支持 ctx 取消：等待者从切片队列摘除并后移后继，名额立即归还。
- FIFO 发放，避免饥饿；mutex + 条件唤醒实现。

## 5. 四条路径归还额度

成功、失败、超时、panic 都会让真实调用终结，占用的在途名额必须释放，否则名额单调泄漏，
最终 N→0 全部请求被拒。用 `defer release()` 覆盖，timeout.Do 内部 `recover()` 把 panic
转成 Class=Retryable 的错误返回，包装器不被击穿。

## 6. 熔断状态机

- CLOSED：连续失败 ≥ Threshold，或 样本数 ≥ MinSamples 且 失败率 > RateThreshold → OPEN。
- OPEN：now ≥ openedAt+cooldown → HALF_OPEN；时钟回拨（now 变小）不得提前迁移，
  返回 ErrClockBackwards 且状态不变。
- HALF_OPEN：放行至多 Probe 个并发探测（令牌计数），其余直接 ErrBreakerOpen；
  探测全成功 → CLOSED；任一失败 → OPEN，cooldown = min(cooldown*2, MaxCooldown)。
- 迁移在互斥区内以"先复查状态再 CAS"方式进行，并发失败只迁移一次、只加倍一次。

## 7. timeout

`Do(ctx, d, fn)`：d>0 时派生带 deadline 的 ctx，goroutine 执行 fn；
先返回者为准，超时返回 ErrTimeout。时钟仅影响熔断冷却判定（可注入），
单次调用时限用真实计时器（避免模拟时钟驱动 goroutine 的复杂度）。

## 8. 统计自洽口径

Total=S+F+Rb+Rh；Real=Total−Rb−Rh；Σ F_class = F。
四类可判定错误：ErrBreakerOpen / ErrBulkheadRejected / ErrTimeout / ErrClockBackwards，
均以哨兵错误 + errors.Is 判别。阈值 ≤ 0、容量 ≤ 0 等非法配置构造即报错。

## 9. 包划分

classify（分类+Clock+哨兵错误）、bulkhead、timeout、breaker、stat（组装 Protector
与口径计数）、cmd/demo。测试表驱动、按包合并，共 9 个 .go 文件以内。
