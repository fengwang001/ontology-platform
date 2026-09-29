# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 多分区全局位点推进器（`progress` 包）

`progress.Tracker` 汇总多个分区独立确认的位点，对外给出一个全局安全水位：

- 分区通过 `Register(id, start)` 注册并给定起始位点；`Report(id, pos)` 报告确认位点，位点只进不退（与当前位点相等视为幂等，不写入历史）；`Finish(id)` 宣告完结，以当前确认位点作为最终位点。
- **全局位点取所有未完结分区确认位点的最小值**。任一未完结分区停滞，全局位点就被钉在该分区位点上，其他分区再怎么前进都不能越过它。
- **完结分区立即移出最小值集合**：分区 `Finish` 后其位点不再参与全局位点计算，阻塞随之解除；全部分区完结后全局位点视为无限大（`MaxPosition`，`Global()` 第二返回值为 `true`）。
- 以下操作被整体拒绝，返回可区分的哨兵错误，且失败不改变任何分区位点、完结标记与全局位点：
  - 未注册分区：`ErrPartitionNotRegistered`
  - 位点回退（新位点小于已确认位点）：`ErrPositionRolledBack`
  - 对已完结分区再报告/完结：`ErrPartitionFinished`
  - 重复注册：`ErrPartitionExists`
  - 活跃分区数超过 `NewTracker(maxActive)` 上限：`ErrTooManyActivePartitions`
  - 空分区 ID 等非法参数：`ErrInvalidArgument`
- 所有读路径（`Global`、`Final`、`State`、`Snapshot`、`History`）均在读锁下执行，可与写操作及彼此并发；`Snapshot()` 返回一次持锁内拷贝出的一致性快照。约定生命周期为“先注册、后报告/完结、不重开”，此约定下全局位点单调不减。

### 用重放历史核对结果（本地验证）

每次真正生效的注册/推进/完结都会追加到操作历史（幂等报告不记录）。可从历史重建实例并逐字段比对：

```go
history := tracker.History()
replayed, snap, err := progress.Replay(history, maxActive)
if err != nil || !progress.EqualSnapshot(tracker.Snapshot(), snap) {
    panic("replay mismatch") // 结果必须可复现
}
```

命令行验证：

```bash
go test -race -v ./progress   # 日志逐条打印操作、各分区位点、全局位点与判定依据
```
