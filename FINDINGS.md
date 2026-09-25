# FINDINGS — 测试观测记录

## 舱壁与超时（bulkhead / timeout 测试组）

- 500 协程、N=8：历史峰值实测 8，恒 ≤ N；全部结束后在途归零。
- N=1、Q=1：第 N+Q+1=3 个请求立即返回 `ErrFull`，注入时钟读数前后相等（未推进）。
- 等待中取消：waiter 以 `context.Canceled` 退出，等待数归 0，归还后可用额度回 1。
- N=1：第二个 Acquire 在持锁期间 50ms 内不通过，释放后立即通过，退化为串行。
- 成功/失败/超时/panic 四路径各 1000 次：可用额度回满 4，panic 被 `timeout.PanicError` 捕获未击穿。

## 熔断状态机迁移（breaker 测试组）

（待补）

## 统计等式（stat 测试组，5 万次随机调用后）

（待补）
