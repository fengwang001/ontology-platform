# FINDINGS：测试结论记录

## 表 ①：A→B→C（A 失败）+ 并行慢任务 S 的四类状态分布

| 任务 | FastFail 状态 | FastFail 原因指向 | BestEffort 状态 | BestEffort 原因指向 |
|---|---|---|---|---|
| A（返回错误） | Failed（带原始错误） | 自身 A | Failed（带原始错误） | 自身 A |
| B（A 下游，未开始） | Skipped | A | Skipped | A |
| C（B 下游，未开始） | Skipped | **A 而非 B** | Skipped | **A 而非 B** |
| S（旁支慢任务） | **Canceled**（started=true，非 Skipped） | —— | Success（跑完） | —— |

分布差异：FastFail 有 Canceled 且 Success 更少；BestEffort 无 Canceled，不依赖
失败任务的分支（S）跑完。两模式 Skipped 的 origin 均指向最初失败的 A。
（`sched.TestStates` 表驱动两模式各 4 任务断言通过）

## 表 ②：故障注入结论

| 注入 | 结论 |
|---|---|
| 环（含自环） | 执行前由 `graph.Check` 检出；`CycleError.Path` 逐边都在输入中且首尾闭合；`errors.Is(err, ErrCycle)` 为真；`sched.Run` 直接返回该错误不执行任何任务 |
| 任务 panic | `exec.Run` recover 后转为 `*fail.PanicError`（`errors.Is(err, ErrPanic)` 为真、payload 保留）；调度器不崩溃，任务记 Failed，报告带 panic 原文 |
| 多任务同时失败 | F1、F2 并行同失败，报告**全部**记录为 Failed 且各带原始错误；各自下游 D1/D2 记 Skipped，origin 分别指向各自最早的失败祖先 F1/F2 |
| 取消后写回 | 慢任务无视取消、迟到 150ms 后仍返回 nil，结果被丢弃，状态保持 **Canceled**（started=true），不翻转为 Success |
