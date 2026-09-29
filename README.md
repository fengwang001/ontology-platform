# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多分区全局位点推进器（`watermark` 包）

多分区汇总流的全局安全水位推进器，位于 `watermark/`，并发安全。

### 位点与完结规则

- 每个分区 `Register` 时给定起始位点，之后独立 `Report` 确认位点；位点只进不退，相同位点重复报告幂等。
- 全局位点 = **所有未完结分区确认位点的最小值**；一个未完结分区都没有时视为**无限大**（`Global() 返回 infinite=true`）。
- 任一未完结分区停滞，全局位点就被钉在该分区的位点上；该分区 `Finish` 完结后，从最小值集合中移出，阻塞才解除。
- `Finish` 记录最终位点（不得低于已确认位点），相同最终位点重复完结幂等；已完结分区不可再报告。
- 为保证全局位点单调不减，新分区起始位点若低于当前有限全局位点会被拒绝（所有未完结分区完结后允许重新注册，此时全局为无限大）。

### 失败原因（可区分、整体拒绝）

所有失败都是事务式的：一次失败不改变任何分区位点、完结标记、历史与全局位点。

- `ErrPartitionNotFound`：分区未注册即报告/完结
- `ErrPartitionExists`：重复注册同名分区
- `ErrOffsetRegressed`：报告或完结位点回退
- `ErrPartitionFinished`：对已完结分区执行操作
- `ErrTooManyPartitions`：活跃/在册分区数超过 `New(maxActive)` 上限
- `ErrStartBeforeGlobal`：新分区起始位点低于当前全局位点

### 并发与可复现

- `Snapshot()` 在读锁内一次性拷贝全部字段，是某一时刻逐字段相同的完整视图；`Global()`、`FinalOffsets()`、`SelfCheck()` 均为只读，可并发调用。
- 每次成功的写操作按序记录为 `Event` 历史；幂等报告不入历史。
- `Replay(history, maxActive)` 按序重放历史即可重建完全相同的推进器与全局位点，结果始终可复现；历史含非法事件时整体失败。
- `SelfCheck()` 校验：确认位点不低于起始位点、完结分区 final==confirmed 且移出最小值集合、全局位点等于未完结分区最小值。

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

### 本地核对全局位点（重放历史法）

`watermark` 包的单测日志会逐操作打印：操作内容、各分区的起始/确认/最终位点与完结状态、全局位点、以及判定依据（例如 “b 停滞在 0，全局被钉在 min(5,0)=0”）。建议按下面方式人工核对：

```bash
# 详细日志：观察每一步的分区位点、全局位点与判定理由
go test -race -v ./watermark

# 只看重放核对用例
go test -race -v -run TestReplayReproduces ./watermark
```

核对步骤：

1. 按日志中的操作顺序，在纸上/表格里维护每个未完结分区的确认位点。
2. 每步后取这些位点的最小值，与日志中的 `global` 对比；所有分区完结后应为 `infinite`。
3. 用 `Tracker.History()` 导出操作历史，调用 `watermark.Replay(history, maxActive)` 重建实例。
4. 比较原实例与重放实例的 `Snapshot()`（含 `Partitions`、`Global`、`GlobalInfinite`）和 `FinalOffsets()` 必须逐字段相同；`TestReplayReproduces` 已自动完成该比对。
5. 任一失败场景（回退、未注册、已完结操作、超上限）后，快照应与失败前完全一致，可对照 `TestRejectionsAreAtomic` 的日志确认状态未变。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
