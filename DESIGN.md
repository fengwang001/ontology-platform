# 设计推导：熔断 + 舱壁 + 时限的调用保护器

## 0. 分层与调用顺序

一次受保护调用的完整路径：`stat.Total++` → `breaker.Allow()` → `bulkhead.Acquire()` → `timeout.Do(fn)` →
`classify.Classify(err)` → `breaker.Record(...)` → `stat` 记账 → `bulkhead.Release()`。

## 1. 为什么熔断计数只算「真实调用」

失败率的定义必须是「真实到达下游的调用中失败的比例」，即 `失败数 / 真实调用数`。
反证：若把熔断拒绝也计入失败，则熔断一旦打开，每次拒绝都把失败率推向 100%，
分母里全是自己的拒绝，永远没有任何样本能让失败率回落 —— 熔断被自己锁死，永远无法半开。
同理，被舱壁拒绝的调用也从未触及下游，同样不计入。
因此口径：**只有拿到舱壁额度、真正执行了 fn 的调用才进入熔断统计与失败率分母**；
熔断拒绝、舱壁拒绝各自单独计数（`stat` 的 RejectedByBreaker / RejectedByBulkhead），
与成功/失败互斥，保证等式 `总请求 = 成功 + 失败 + 熔断拒绝 + 舱壁拒绝` 恒成立。

## 2. 为什么熔断在舱壁之前

熔断打开的语义是「下游已知故障，调用几乎必然失败」。此时正确的动作是**立刻、零成本**拒绝：
- 若熔断在前：拒绝不占用并发额度、不进等待队列，额度留给恢复后的探测与其他逻辑；
- 若舱壁在前：每个被拒请求仍要排队/占额度，故障期间舱壁被「注定失败的请求」塞满，
  恢复后第一批真实调用反而被自己的拒绝者挤死，且等待队列上限 Q 会被无效消耗。
故规定顺序为 **熔断 → 舱壁**，可检验推论：熔断打开期间舱壁在途数恒为 0。

## 3. 为什么四条路径都必须归还额度

额度是计数信号量：Acquire 成功即占用一份，唯一归还点是 Release。
调用结局只有四种：成功、业务失败、超时、panic。任一路径漏还，额度单调泄漏，
N 次泄漏后可用额度归零，此后所有请求被拒 —— 舱壁从「隔离器」退化为「永久拒绝器」。
因此实现上用 `defer Release()` 紧跟在 Acquire 成功之后，使四条路径（含 panic 转换）
都必然经过归还点。测试对四条路径各跑 1000 次后断言可用额度回到满值。

## 4. 不可重试错误是否计入熔断

定义：**超时与可重试错误计入熔断失败；不可重试错误（如参数非法）不计入熔断**。
论证：熔断保护的是「下游健康状况」。不可重试错误是调用方契约错误（4xx 语义），
重发一万次也失败，与下游容量/健康无关；若计入，一个写错参数的调用方就能把
全体调用方的熔断器打开，造成误伤。但它仍是「真实调用」的失败，必须计入
`stat` 的失败数与分类细分，保持统计等式自洽 —— 只是不喂给熔断状态机。
`classify.IsBreakerFailure(err)` 是该口径的唯一判定入口。

## 5. 熔断状态机（注入时钟 `Clock`，可回拨检测）

- 关闭 → 打开：连续失败 ≥ `ConsecutiveFailures`，**或**（窗口样本 ≥ `MinSamples` 且失败率 > `FailureRate`）。
  窗口为最近 64 次真实结果的环形缓冲，进入打开时清空。
- 打开 → 半开：`now - openedAt ≥ cooldown`。冷却期从 `BaseCooldown` 起，每次半开失败加倍，
  上限 `MaxCooldown`。**时钟回拨**（now < openedAt）视为环境异常：返回 `ErrClockRegression`，
  状态不变、不进入半开。
- 半开 → 关闭：连续成功探测数达到 `HalfOpenProbes`。
- 半开 → 打开：任一失败立即回到打开，且冷却加倍（≤ MaxCooldown）。
- 半开并发控制：在途探测数 < `HalfOpenProbes` 才放行，否则拒绝；
  迁移用「预期状态 + 单写者」保证并发下只迁移一次（用计数器断言）。

## 6. 舱壁精确语义

- 在途数恒 ≤ N，非导出峰值计数器经 `Peak()` 暴露供测试。
- 等待队列上限 Q：第 N+Q+1 个请求**立即**返回 `ErrBulkheadFull`，不推进时钟、不阻塞。
- 等待中被 ctx 取消：从队列摘除并归还等待名额（不占用在途额度）。
- 每个下游一个 `Bulkhead` 实例，天然互不影响。

## 7. 时限与 panic

`timeout.Do` 在独立 goroutine 执行 fn，`select` 先到者胜；超时返回 `ErrTimeout`。
fn 内 panic 被 recover 捕获并转换为 `ErrPanic`（包装 panic 值），不击穿包装器。
超时后 fn 的迟到 panic 在受控 recover 中丢弃，不会 crash 进程。

## 8. 统计口径（stat）

原子计数：Total / Real / RejectedByBreaker / RejectedByBulkhead / Success /
Failed{Retryable, NonRetryable, Timeout, Panic}。自洽等式（任意交错下成立）：
`Total = Success + Failed总数 + RejectedByBreaker + RejectedByBulkhead`；
`Real = Total - RejectedByBreaker - RejectedByBulkhead`；`Failed总数 = 四类细分之和`。
