# 实验结论

## 表 1：四类状态分布（图：A→B→C，S 为并行慢任务，A 失败）

| 任务 | FailFast | BestEffort | 原因指向 |
|---|---|---|---|
| A | 失败 | 失败 | 原始错误 |
| B | 被跳过 | 被跳过 | A |
| C | 被跳过 | 被跳过 | A（非 B） |
| S | 被取消 | 成功 | FailFast 下为 ErrCanceled |

实测（`fail_test.go`）：FailFast 分布 1/1/2/1（成功/失败/跳过/取消），
BestEffort 分布 1/1/2/0；Canceled 只在 FailFast 出现，是两种模式的可判定差异。
取消后 S 仍返回 nil，结果被丢弃，状态保持 Canceled。

## 表 2：故障注入结论

| 注入 | 结论 |
|---|---|
| 环（含自环） | 执行前检出；`CycleError.Path` 逐边属于输入且首尾闭合（graph 测试通过） |
| panic | recover 捕获转为 `PanicError`（`errors.Is` 达 `ErrPanic`），调度器不崩，下游被跳过 |
| 多任务同时失败 | 报告记录全部失败任务（各自原始错误）；被跳过者原因指向本分支最早失败 |
| 取消后写回 | 被丢弃，状态保持「被取消」；调度器仍等待其 goroutine 退出（无泄漏） |
