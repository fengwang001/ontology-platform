# 测试结论

## 表 1：`A→B→C` + 并行慢任务 D（D 与 A 并行，无依赖）

| 任务 | FastFail | BestEffort |
| --- | --- | --- |
| A | Failed（根因 A，原始错误保留） | Failed（根因 A） |
| B | Skipped，原因→A（非 B 的直接上游 A 本身） | Skipped，原因→A |
| C | Skipped，原因→A（跨 B 指向最初失败） | Skipped，原因→A |
| D（慢） | Canceled（已开始，被 ctx 中止；非 Skipped） | Succeeded（无失败上游，继续跑完） |

关键：C 的跳过原因指向 A；D 在两种模式下分别为 Canceled/Succeeded，差异即模式语义。

## 表 2：故障注入结论

| 注入 | 结论 |
| --- | --- |
| 环（含自环） | 执行前检出；返回闭合路径，逐边都在输入中，首尾相同（待补测试日期） |
| panic | 待补 |
| 多任务同时失败 | 待补 |
| 取消后写回 | 待补 |
