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

## 并行对齐快照导出器（`ontology` 包）

`ontology.Exporter` 支持向 N 个分区并发投递事件，并导出全局一致快照。

### 对齐点规则

- 每个分区独立投递，同分区事件序号（切片下标）连续。
- **对齐点 = 所有分区已投递事件数的最小值**。
- 快照只纳入每个分区的前 `对齐点` 条事件，按分区号顺序（0 → N-1）拼接；
  同键跨分区时后写覆盖前写（分区号更大的分区胜出）。
- 对齐点只随落后分区的推进整跳变，因此任一快照内每个分区贡献的事件条数
  都**恰等于该快照的对齐点**，不存在“半个分区”被纳入的中间态。
- 快照无副作用，同一内部状态下重复导出结果完全一致（可复现）。

### 背压上限规则

- 分区的待定缓冲 = 该分区已投递事件数 − 当前对齐点（即超出对齐点的条数）。
- 待定缓冲达到构造时给定的 `pendingLimit` 后，再投递该分区会被拒绝，
  返回包装了 `ontology.ErrBackpressure` 的错误；**被拒绝事件不落盘，
  各分区事件数与待定缓冲均不变**。
- 其他分区不受影响；落后分区追上来抬高对齐点后，待定缓冲自动释放。
- 另两类整体拒绝原因与背压错误可通过 `errors.Is` 区分：
  `ontology.ErrPartitionOutOfRange`（分区号越界）、
  `ontology.ErrEmptyKey`（空键）。
- 注意 `pendingLimit=0` 时任何分区都不能领先对齐点，属于齐步推进的退化配置。

### 并发语义

投递、快照（`Snapshot`）、自检（`SelfCheck`）及各计数查询均可并发调用；
内部以读写锁保护，快照与自检在同一把读锁内完成，读到的是某次投递序列下的
完整点态。

### 本地验证方法

用“取各分区等长前缀拼接”独立核对快照：

1. 取快照的对齐点 `A = min(各分区 delivered)`。
2. 对每个分区取前 `A` 条事件，按分区号 0 → N-1 顺序写入一个 map，
   同键后写覆盖前写。
3. 该 map 必须与 `Snapshot().Entries` 完全一致，且每个分区恰好贡献 `A` 条。

单测 `TestEqualContribution`、`TestConcurrentSnapshotAndAppend` 即按此方法核对，
日志会打印每次投递、各分区 `delivered/pending`、对齐点及拒收判定依据：

```bash
go test -race -v -run 'TestBackpressureReject|TestAlignmentIsMinimum|TestEqualContribution|TestRejectCauses|TestZeroPendingLimit|TestConcurrentSnapshotAndAppend' ./ontology/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
