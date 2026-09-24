# FINDINGS

## ① 状态分布表（图：A→B→C，另加并行慢任务 D）

| 任务 | FailFast 状态/原因 | BestEffort 状态/原因 |
|---|---|---|
| A | failed / 原始错误 boom | failed / 原始错误 boom |
| B | skipped / reason=A（从未启动） | skipped / reason=A（从未启动） |
| C | skipped / reason=A（穿透 B 指向 A） | skipped / reason=A（穿透 B 指向 A） |
| D（并行慢任务） | canceled / 已启动，ctx 被取消 | success / 独立分支继续跑完 |

## ② 故障注入结论

| 注入 | 结论 |
|---|---|
| 环（含自环） | 执行前检出，CycleError.Path 首尾闭合且每跳均为输入边；已单测验证。 |
| 并发与判定 | 500 无依赖任务、上限 8：峰值 8；决策 500 ≤ 4(V+E)=2000；-race 通过。 |
| panic | 任务 goroutine 内 recover，转 failed 且 errors.Is(err, ErrTaskPanic)，原始信息保留；调度器不崩。 |
| 多任务同时失败 | BestEffort 下 X、Y 均 failed 全部保留；共同下游 Z=skipped，reason 取最小失败祖先 X。 |
| 取消后写回 | S 已 started 被预标 canceled；其 20ms 后迟到的成功返回被 Complete 拒绝，终态仍 canceled。 |
| 确定性 | 边序打乱 20 次 + 随机延迟 20 次，状态摘要逐次相同；C=1 串行与 C=8 并行摘要相同。 |
| goroutine | 全成功 / FailFast / BestEffort 三路径后 NumGoroutine 均回基线（容差 1）。 |
