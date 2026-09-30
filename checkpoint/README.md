# checkpoint：检查点协调器

为固定数量（N）的任务驱动检查点的触发、确认、超时、吞并、保留与恢复选点。
所有公开方法（`Trigger` / `Confirm` / `Advance` / `Restore` / `Snapshot` / `Get`）
都可并发调用：内部以一把互斥锁把每个操作整体串行化，因此任意并发交错下的
结果都与「逐事件串行推演」完全一致；同一操作序列重放结果完全相同。

## 创建参数

| 参数 | 含义 |
| --- | --- |
| `Tasks` | N，每个检查点需 N 个不同任务确认才完成 |
| `MaxConcurrent` | 进行中检查点的并发上限 |
| `MinInterval` | 触发距最近一次完成时钟的最小间隔（从未完成则不限） |
| `Timeout` | 超时：时钟 `>= 触发时钟 + Timeout` 即中止 |
| `MaxConsecutiveTimeout` | 可容忍连续超时数，超过则协调器失败 |
| `Retention` | R，已完成的只保留编号最大的 R 个 |

## 状态迁移与计数规则

- 时钟只由 `Advance` 前进且不得回退；超时只在推进时判定。
- `Trigger` 在当前时钟分配从 1 起递增的编号，被拒不占号，编号连续无空洞、
  恢复后延续不复用。
- 检查点生命周期：`in_progress -> completed`（N 个不同任务确认齐）或
  `in_progress -> aborted`（恰好终态一次），中止原因：
  - `timeout`：推进时判定，同一次推进内按编号升序逐个处理，**计入**连续超时数；
  - `subsumed`：更大编号完成时吞并所有编号更小的进行中检查点，**不计入**；
  - `restored`：恢复时中止全部进行中检查点，**不计入**；
  - `failed`：连续超时数超过容忍数时协调器转失败，其余进行中检查点连带中止，**不计入**。
- 连续超时数：超时中止 +1；任一检查点完成或恢复时清零。
- 协调器失败后触发一律拒绝；`Restore` 返回保留集中编号最大者、中止全部进行中
  检查点、清零连续超时数并使协调器回到正常。
- 保留集只留已完成编号最大的 R 个，超出即淘汰最小编号。

## 错误优先级（各按此序只报第一个，被拒绝的操作不改变任何状态）

- `Advance`：`clock_backward`（时钟回退）。
- `Trigger`：`coordinator_failed` → `concurrency_full` → `interval_too_short`。
- `Confirm`：`task_out_of_range` → `not_found` → `already_ended` → `duplicate_confirm`。
- `Restore`：`no_checkpoint`（保留集为空）。

所有拒绝都返回 `*checkpoint.Error`，含 `Op` / `Reason` / `Detail` 字段，
可按 `Reason` 区分原因。

## 日志

每个操作都打印输入、输出与判定依据，格式为：

```
op=<操作> in=<输入> out=<accept|complete|reject|ok ...> reason=<拒绝原因> basis="<判定依据>"
```

例如超时判定打印 `basis="clock=10 >= started_at=0 + timeout=10"`，
间隔拒绝打印 `basis="clock=4 - last_completed_at=0 = 4 < min_interval=5"`。

## 本地验证

```bash
go vet ./...
go test -race -count=1 -v ./checkpoint/
```

测试覆盖：吞并、同一次推进内多个超时逐个计数并转失败、确认恰在超时推进前后、
间隔从完成时刻算起、保留淘汰与恢复后编号延续、错误优先级、时钟回退拒绝、
并发下同任务确认只成功一次 / 并发触发不超上限且编号连续无空洞、
混合并发（-race）、同一操作序列重放结果一致、日志包含输入/输出/判定依据。
