# 保留加回填调度器（reservation + backfill）

`scheduler` 包为 `N` 个同构节点实现多节点作业的"保留 + 回填"调度。时间
完全由注入时钟推进（`Scheduler.Advance`），调度器自身不读取墙钟，因此
相同的提交 / 结束 / 时钟序列重放必然得到相同的启动序列。

## 数据模型

- `Job{ID, Nodes, Duration}`：作业标识、所需节点数、预估时长（整数时钟
  tick）。
- 作业在 `start + Duration` 时刻被强制结束，也可在运行中被 `Finish` 提前
  结束。
- 每次 `Submit` / `Finish` / `Advance` 都触发一次调度扫描。

## 调度算法

1. **立即启动队首**：按提交顺序，只要"当前已用节点 + 队首所需 ≤ N"就启动
   队首（原因 `immediate`），直到队首放不下为止。
2. **为受阻队首计算保留（影子时刻）**：把全部运行作业按预估结束时刻升序
   排列，并列按作业标识升序，作为唯一确定的释放顺序；然后逐个释放（只在
   纸面上累计释放节点，不真的结束作业），直到放下队首。队首最早可行的
   启动时刻（**shadow time，影子时刻**）等于所需释放集合中最后一个作业的
   结束时刻。
3. **富余节点（spare）**：在影子时刻、满足队首之后仍然空闲的节点数

   ```
   spare = N - 队首所需 - 影子时刻仍在运行的作业节点之和
   ```

   被释放集合是结束有序序列的一个前缀，所以"影子时刻仍在运行"恰好等于
   "结束时刻严格晚于影子时刻"。
4. **回填扫描**：保持提交顺序扫描队首之后的其余作业。作业可回填当且仅当
   **现在就放得下**，并且满足以下两类条件之一：
   - **时间条件**（`backfill:ends-by-shadow`）：`now + Duration ≤ shadow`，
     即影子时刻之前（含恰好影子时刻结束）它已把占用的节点全部归还；
   - **富余条件**（`backfill:within-spare`）：`Nodes ≤ spare`，此时从
     `spare` 中扣除该作业的节点数。

   两个条件都不满足的作业留在队列中，扫描继续检查后面的作业（不会因为
   某个作业放不下就停止）。

## 正确性性质

- **不超分**：任意时刻运行作业的节点之和 ≤ N；回填第一关就是"现在放得
  下"。
- **保留上界**：任一作业成为队首时算出的影子时刻是其实际启动时刻的上
  界。时间类回填在影子时刻前归还全部节点；富余类回填使用的是影子时刻服务
  队首后确认多余的节点；提前结束 / 强制结束只会释放节点并重算保留，因此
  实际启动只可能更早。
- **可复现**：释放顺序（结束时刻、再标识）、提交顺序扫描、强制结束顺序
  全部确定；无随机、无 map 迭代依赖。
- **并发安全**：`Submit` / `Finish` / `Advance` / `Query` 共用一把互斥
  锁，可并发调用。

## 拒绝原因（整体拒绝，状态不变）

| 情形 | 错误 |
| --- | --- |
| 集群节点数 ≤ 0 | `ErrInvalidNodes` |
| 作业节点数 ≤ 0 或 > N | `ErrInvalidJobNodes` |
| 预估时长 ≤ 0 | `ErrInvalidDuration` |
| 作业标识重复（历史上出现过即算） | `ErrDuplicateID` |
| 结束一个不在运行中的作业 | `ErrNotRunning` |
| 时钟回拨（`Advance` 到更早时刻） | `ErrClockRollback` |

被拒绝的操作在任何状态变更之前返回，不会改动队列或运行集合。

## 日志

向 `New` 传入 `*log.Logger`（传 `nil` 使用默认 stderr logger）。每个操作
都会记录：

- `input ...`：操作输入与当前时钟；
- `output ...`：返回的启动 / 结束事件或拒绝原因；
- `decision ...`：判定依据，含 `head-blocked`、`reservation shadow=…
  spare=… release-order=…`、`backfill-time`、`backfill-spare`、
  `backfill-skip`、`force-finish`、`start reason=…`。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 重复执行
go test -race -count=3 ./...

# 详细查看场景与随机负载
go test -race -v ./scheduler/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

关键测试：

- `TestBackfillEndsExactlyAtShadow`：恰在影子时刻结束的作业可回填；
- `TestBackfillSpareSharing`：多个回填作业按提交顺序分摊富余节点，富余
  耗尽后即使现在放得下也不得回填；
- `TestEarlyFinishRecompute`：提前结束触发保留重算，队首早于原影子时刻
  启动；
- `TestForceFinish`：到达预估时长立即强制结束；
- `TestRandomLoadReservationBound`：多种随机负载下独立复算影子时刻，
  恒有 `start ≤ shadow`，且不超分、作业不超期；
- `TestReplayDeterminism`：相同序列重放产生逐字节相同的启动序列；
- `TestConcurrentStress`：并发提交 / 结束 / 推进 / 查询的竞态压测。
