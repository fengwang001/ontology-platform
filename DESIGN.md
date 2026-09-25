# 熔断与舱壁调用保护器 — 设计推导

## 1. 分层顺序：熔断在前，舱壁在后

请求流水线固定为：`breaker → bulkhead → timeout → 真实调用`。

若舱壁在前，熔断打开期间被拒绝的请求仍要先竞争并发额度/进等待队列：拒绝也排队，
额度被注定不会执行的请求占住，真正偶发的探测与恢复反而被饿死。熔断在前则在下游
已知故障时立即返回拒绝，**不占用任何舱壁名额**，舱壁占用数在熔断打开期间恒为 0。
统计上先判熔断、再判舱壁，一个请求只会被归入一类拒绝，互斥不重复。

## 2. 熔断计数只算「真实调用」

熔断的失败率是「下游真实健康度」的估计，样本空间只能是真正触达下游的调用。
被熔断拒绝是保护器自身的决策结果，不含下游信息；若计入分母甚至计为失败，则
打开 → 拒绝 → 失败率 100% → 永远无法满足恢复条件，形成自锁。因此：

- 被熔断拒绝、被舱壁拒绝都不进入熔断窗口，也不进成功/失败计数；
- 熔断窗口的样本 = 通过了熔断与舱壁、真正执行（含 panic、超时）的调用；
- 恢复只需冷却后半开探测，探测全部成功即关闭。打开后拒绝 1000 次对窗口零影响，
  半开后一次（ProbeCount 次内）成功即可关闭。

## 3. 错误类别与熔断定义

四类可 `errors.Is` 判定的错误：`ErrRetryable`、`ErrNonRetryable`、`ErrTimeout`，
以及保护器拒绝错误 `breaker.ErrOpen`、`bulkhead.ErrRejected/ErrCanceled`。
分类规则：`ErrTimeout` 归为超时；`ErrNonRetryable` 是调用方语义错误（如 4xx、
参数非法），重试无意义，**不计入熔断失败**（计为一次真实调用与失败统计，但不喂
给熔断窗口）；`ErrRetryable` 与 panic（下游崩溃/连接中断，通常可重试）计入熔断失败。

## 4. 舱壁语义

每下游独立 Bulkhead：N 个在途额度 + Q 个等待位。在途数恒 ≤ N，内部计数器记峰值。
第 N+Q+1 个请求立即返回 `ErrRejected`（注入时钟下时间不前进——拒绝是纯同步判定）。
等待者被 `ctx` 取消时从等待队列摘除并归还名额，返回 `ErrCanceled`。
所有路径归还：成功、失败、超时、panic 都在 `defer Release()` 覆盖内，各跑 1000 次
后可用额度必须回到 N，否则额度泄漏到 0 会永久全拒。不同下游各自持锁与计数，
X 耗尽额度时 Y 的成功率不受影响。

## 5. 熔断状态机

- 关闭→打开：连续失败数 ≥ ConsecutiveThreshold，或窗口内失败率 > RateThreshold
  且样本数 ≥ MinSamples；迁移用 CAS 保证只发生一次，冷却只初始化一次。
- 打开→半开：`Now().Sub(openedAt) ≥ cooldown`；**时钟回拨**（Now 早于 openedAt）
  返回 ErrOpen 且不迁移、不重置 openedAt，杜绝提前半开。
- 半开→关闭：占用探测名额的调用在 ProbeCount 次以内全部成功。
- 半开→打开：任一探测失败立即回打开，`cooldown = min(cooldown*2, MaxCooldown)`
  （退避加倍仅此一处，不做调用重试）；重置 openedAt 与探测计数。
- 半开期探测名额用尽后其余并发请求直接 ErrOpen；100 协程并发时通过数恰好
  = ProbeCount。

## 6. 统计口径

计数器：Total、Real、BreakerRejected、BulkheadRejected、Success、
Failures{Retryable,NonRetryable,Timeout,Panic}。每请求恰好一条结局：

- Total = Success + ΣFailures + BreakerRejected + BulkheadRejected
- Real = Total − BreakerRejected − BulkheadRejected
- ΣFailures(按类别) = Failures 总数

成功/失败只由真实调用产生，被拒请求只动拒绝计数器，因此三条等式在任意操作序列下
都是同一互斥分类的两种写法，天然自洽。
