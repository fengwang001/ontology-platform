# 实测结论

## 各组测试

| 包 | 用例 | 结论 |
|---|---|---|
| classify | 三类错误/包装错误/未知错误归 Fatal | PASS：errors.Is 全可区分，Timeout/Fatal 判定正确 |
| bulkhead | 500 协程 N=8；队列满立即拒绝；等待取消；四路径×1000；N=1；X/Y 隔离；非法配置 | PASS：Peak=8 上限精确；拒绝耗时 <1ms；取消后 Available 回满；四路径无泄漏；Y 成功 100/100（-race） |
| timeout | 成功/错误透传/截止/父 ctx 取消/panic/非法时限 | PASS：截止错误 Is(ErrTimeout)；panic 转 Retryable 不击穿（-race） |

## 熔断五条迁移（触发条件 / 用例 / 冷却序列）

待 breaker 测试后填写。

## 5 万次随机调用统计等式实测

待 stat 测试后填写。
