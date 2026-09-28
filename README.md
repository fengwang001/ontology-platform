# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 幂等落库组件 `idempotent`

`idempotent` 包把至少一次（at-least-once）投递的批次，按“分区 + 位点”
去重后幂等累加到结果表，保证崩溃或重投后不漏计、不重复计。

### 水位与去重规则

- 每个分区独立维护一个**水位（watermark）**：该分区已生效记录的最大位点。
- 位点严格大于水位的记录**生效**：值按 key 累加到结果表，水位推进到该位点。
- 位点不大于水位的记录判定为**重复**并丢弃，重复计数 `Duplicates` 加一。
- 分区首次出现时没有水位，位点 `0` 是合法的首条记录（不会被误判为重复）。
- 水位只按分区推进，与位点是否连续无关，允许跳号。
- 同一批次内同一分区的位点必须**严格递增**（允许与其他分区的记录交错）；
  出现相等或回跳则整批拒绝。跨批次不要求位点有序——重投旧位点只会被判重。

### 拒绝原因（可区分，`errors.Is` 判定）

- `ErrNegativePartition`：分区为负。
- `ErrNegativeOffset`：位点为负。
- `ErrEmptyKey`：记录键为空。
- `ErrOutOfOrder`：同批同分区位点未严格递增。
- `ErrTooManyPartitions`：批次引入的新分区会使分区总数超过 `MaxPartitions`
  （默认 `DefaultMaxPartitions = 1024`）。

被拒绝的批不写盘、不改内存，结果表、水位、重复数均保持不变。校验在任何
累加之前完成，因此批内即使只有一条非法记录，整批也不会产生部分生效。

### 原子提交与重启

- 一次 `Apply` 先在拷贝出的候选状态上完成全部判定，再把**结果表 + 全部分区
  水位 + 累计重复数**作为一个整体写入状态文件。
- 落盘采用“临时文件 + `fsync` + 原子 `rename` + 目录 `fsync`”，崩溃后要么
  看到上一个完整状态，要么看到新的完整状态，不会出现半个文件。
- 只有落盘成功后内存状态才切换；落盘失败时内存保持旧状态，可安全重试。
- 重启（重新 `Open`）时**仅从状态文件重建**，不保留任何进程内状态；重放
  历史批次只会增加重复计数，结果表与水位不变。
- `Apply` 由互斥锁串行化，可被多 goroutine 并发调用，结果等价于每批只写
  一次，重复数恰好等于多写的记录次数；同一输入序列反复计算输出完全一致。

### 用法

```go
sink, err := idempotent.Open(idempotent.Config{
    Path:          "data/state.json",
    MaxPartitions: 64,                // 可选，默认 1024
    Logger:        slog.Default(),    // 可选
})
if err != nil { /* ... */ }

out := sink.Apply(idempotent.Batch{
    ID: "batch-001",
    Records: []idempotent.Record{
        {Partition: 0, Offset: 10, Key: "orders", Value: 3},
        {Partition: 1, Offset: 0, Key: "orders", Value: 2},
    },
})
switch {
case out.Rejected: // out.Err 包裹上述五种哨兵错误之一
case out.Err != nil: // 落盘失败，状态未变，可重试
default:
    // out.Applied 生效条数；out.Duplicate 本批判重条数
}

snap := sink.Snapshot() // snap.Results / snap.Watermarks / snap.Duplicates
```

每次调用都会通过 `slog` 打印：批次输入（批 ID、记录数）、每条记录的生效或
重复判定及其依据（当前水位、新旧水位、`offset > watermark` /
`offset <= watermark`）、拒绝原因，以及整批原子提交的汇总计数。

### 本地验证

```bash
go test -race -v ./idempotent
go test -race -cover ./...
go vet ./...
gofmt -l .
```

测试覆盖：水位推进与重投判重、丢弃内存后重启重建并再次重投、同批内重复/
乱序位点整批拒绝、五类非法输入且状态不变、并发重投串行化（结果与只写一次
一致、重复数等于多写次数）、同一输入序列多次计算结果完全一致。

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
