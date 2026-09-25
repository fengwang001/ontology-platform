# 测试结论

## 测试组进展
| 测试组 | 内容 | 结果 |
| --- | --- | --- |
| classify | 四类错误分类 + errors.Is + 原始错误保留 | PASS |
| bulkhead | 500 协程 N=8 峰值≤8；队列满立即拒且时钟不动；取消移除；四路径×1000 额度回满；N=1 串行；X/Y 隔离 Y 100/100 | PASS(含 -race) |
| timeout | 成功透传；超时归 Timeout；panic 转 PanicKind 不击穿；非正时长报错 | PASS |
| breaker | 五条迁移；半开 3 探测/100 协程恰放 3；1000 拒绝不影响率；回拨拒绝状态不变；并发迁移恰 1 次 | PASS(含 -race) |
| stat | 待补 | - |

## 表① 熔断状态机迁移
| 迁移 | 触发条件 | 测试用例 | 冷却时长序列 |
| --- | --- | --- | --- |
| 关闭→打开(连续失败) | 连续失败数达 Fails=3 | consecutive failures open | 初始 10s |
| 关闭→打开(失败率) | 样本≥MinSamples=5 且失败率>0.5 | failure rate opens at min samples | - |
| 打开→半开 | now≥openedAt+cooldown | cooldown opens to halfopen... | - |
| 半开→关闭 | 探测全部成功 | 同上；1000 拒绝后一次成功即关闭 | 冷却重置为 10s |
| 半开→打开 | 任一探测失败 | halfopen failure doubles cooldown capped | 10→20→40→40s(封顶 40s) |

## 表② 5 万次随机调用统计不变量实测
| 不变量 | 左值 | 右值 |
| --- | --- | --- |
| 总=成功+失败+熔断拒+舱壁拒 | 待补 | 待补 |
| 真实=总−熔断拒−舱壁拒 | 待补 | 待补 |
| Σ分类失败=失败 | 待补 | 待补 |
