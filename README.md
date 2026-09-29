# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 带检查点的物化视图重建

`materializedview` 包把有序源序列按出现顺序按键计数，生成不可被查询方修改的 `CountView`。

- `BeginRebuild(blockSize)` 创建空的影子副本；块大小必须大于 0。
- `StepRebuild()` 按原序列处理一个块；只有整块完成后才更新检查点。
- `CrashRebuild()` 模拟块之间崩溃：当前未完成块丢弃，已完成块和影子计数保留。
- 崩溃后再次 `BeginRebuild` 会从上一检查点继续，继续使用检查点中的块大小，入参块大小仅在新重建时生效。
- 重建期间 `View`、`Generation` 和 `Snapshot` 都仍读取旧视图，影子副本不会暴露。
- 所有事件处理完并调用 `CommitRebuild` 后，才在同一把状态锁内替换视图并将代数加一。
- `Snapshot` 返回同一个完整提交边界上的代数和视图；重建期间只会读到旧边界，提交后只会读到新边界。

非法操作会返回可区分的哨兵错误，并且拒绝时不修改任何状态：

- `ErrInvalidBlockSize`：块大小小于等于 0。
- `ErrRebuildAlreadyOpen`：已有活动重建时再次开始。
- `ErrNoRebuild`：没有活动重建时步进、提交或模拟崩溃。
- `ErrRebuildIncomplete`：还有源事件未处理完就提交。

### 本地一致性验证

分块重建始终从空影子视图按源顺序重放，因此结果等价于 `ReplaySource(events)` 对完整源序列做一次从头重放，与块大小、崩溃次数和续跑位置无关。本地验证可运行：

```bash
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test -race -v ./materializedview
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test ./...
```

详细测试日志会打印操作名、已处理事件数、影子内容、当前提交视图、代数和判定依据；其中 `TestCrashResumesFromLastCheckpoint` 验证崩溃续跑，`TestOldViewRemainsReadableUntilAtomicSwitch` 验证重建期间旧视图可读，`TestRebuildMatchesReplay` 用 `ReplaySource` 核对最终结果，`TestConcurrentReadsOnlySeeCommittedBoundaries` 验证并发读不会看到半成品。

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
