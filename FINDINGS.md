# FINDINGS

## 表 1：A->B->C 加并行慢任务 S，两种模式下的状态分布

场景：A 在 S 起跑后失败；S 响应 ctx 取消（FailFast 下被取消）。

| 模式 | A | B | C | S |
|---|---|---|---|---|
| FailFast | Failed(原始错误) | Skipped->A | Skipped->A | Canceled |
| BestEffort | Failed(原始错误) | Skipped->A | Skipped->A | Succeeded |

结论：C 的跳过原因越过直接上游 B 指向最初失败的 A；S 已开始执行故为
Canceled 而非 Skipped；模式差异只体现在旁支 S 的终态上。

## 表 2：故障注入结论

| 注入 | 结论 |
|---|---|
| 环（含自环） | 执行前由 Layers 检出；路径逐边在输入中且首尾闭合，errors.Is 命中 ErrCycle |
| 任务 panic | （待补） |
| 多任务同时失败 | （待补） |
| 取消后写回 | （待补） |
