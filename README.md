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

## 双输入检查点屏障对齐（`barrier` 包）

`barrier.Aligner` 为一个有两个输入通道（通道号 `0`、`1`）的算子提供检查点
屏障对齐能力，实现在 `barrier/barrier.go`。

### 阻塞规则

- 每个通道的屏障编号从 `1` 开始递增。通道收到自己的屏障后立即进入阻塞。
- 阻塞期间到达的普通记录与后续屏障进入该通道的有序缓冲，不向下游发出任何
  输出；缓冲的普通记录数受 `New(bufferLimit)` 上限约束（屏障不计数）。
- 未阻塞通道的普通记录到达即处理：累加进状态并立即作为 `record` 输出下发。

### 对齐规则

- 当两个通道都停在同一编号屏障上时发生对齐：
  1. 对当前累加状态拍快照（按 `Key` 排序、逐键的值按处理顺序排列）；
  2. 依次向下游转发通道 0、通道 1 的该编号屏障；
  3. 解除两侧阻塞。
- 因此每个快照恰好包含两侧该编号屏障之前的全部记录，不含任何屏障后数据。

### 重放规则

- 对齐后按**全局到达顺序**合并重放两侧缓冲（事件带单调到达序号）。
- 重放的记录补入累加状态并作为 `record` 输出；重放遇到某通道的下一号屏障
  时，该通道重新阻塞，等待另一通道到达同号屏障再次对齐，由此支持连续多个
  检查点。

### 拒绝规则（可区分原因）

整批（一次 `Apply`）先在状态深拷贝上试运行，任一事件非法则整批拒绝，
累加状态、缓冲、快照与输出流都不改变：

- `ErrInvalidChannel`：通道号不是 `0` 或 `1`；
- `ErrEmptyKey`：普通记录的 `Key` 为空；
- `ErrInvalidBarrierNo`：屏障编号不等于该通道期望的下一个编号（跳号/重复）；
- `ErrBufferLimitExceeded`：缓冲普通记录将超过每通道上限。

### 并发与确定性

- `Apply` 串行化写入；`Snapshots`、`SnapshotAt` 为读锁并发读，快照返回深
  拷贝，读取方的修改不会污染内部状态（逐键一致）。
- 相同输入序列重复计算得到完全相同的输出流与快照（重放顺序由到达序号确定）。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志：输入、输出片段、判定依据）
go test -race -v ./barrier

# 只跑场景 / 并发用例
go test -run 'TestOneSideBlockedAndBuffers|TestAlignmentSnapshotAndReplay|TestReplayHitsNextBarrier|TestInvalidInputs|TestBufferLimitExceeded' -v ./barrier
go test -run 'TestConcurrentReads|TestDeterminism' -race -v ./barrier
```
