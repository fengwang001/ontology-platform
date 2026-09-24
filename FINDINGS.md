# FINDINGS

## ① 熔断状态机五条迁移

| 迁移 | 触发条件 | 测试用例 | 观察到的冷却时长序列 |
|---|---|---|---|
| 关闭→打开(连续) | 连续失败数 ≥ ConsecutiveFailures | 待 breaker 测试 | 初始冷却 |
| 关闭→打开(比率) | 样本≥MinSamples 且 失败率 > Rate | 待 breaker 测试 | 初始冷却 |
| 打开→半开 | 注入时钟经过冷却 | 待 breaker 测试 | — |
| 半开→关闭 | Probe 次探测全部成功 | 待 breaker 测试 | — |
| 半开→打开 | 任一探测失败 | 待 breaker 测试 | 初始冷却 → ×2 → 上限 |

## ② 五万次随机调用统计等式

待 stat 测试填入实测值。

## 分包测试结论

- classify：8 个表驱动用例全部通过；未标记普通错误默认可重试，
  `context.DeadlineExceeded`/`Timeout()bool` 归超时，`context.Canceled` 归不可重试；
  仅 Retryable/Timeout 进入熔断窗口。
- bulkhead：7 组表驱动用例（含 `-race`）全部通过。500 协程 N=8 峰值 ≤8 且
  结束后 Available=8；N+Q+1 在 <10ms 内立即返回 `ErrBulkheadFull`；
  等待中取消返回 `ErrBulkheadCanceled` 且名额回满；N=1 串行峰值恰为 1；
  X 占满不影响 Y；重复 release 幂等；N<=0/Q<0 构造报错。
