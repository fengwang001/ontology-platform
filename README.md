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

## buffer：带背压的批量冲刷变更缓冲

`buffer` 包提供先进先出的变更缓冲，把写入按批量交给下游（`Sink`），
并保证下游收到的条目不丢不重、顺序可复现。

### 两档触发

- **批量触发（优先档）**：缓冲内条数达到 `BatchSize` 立即冲刷一个批量。
- **延迟触发**：最旧条目停留时长达到 `MaxDelay` 时冲刷（通过 `FlushIfDue`
  主动检查，可由定时器驱动；测试用 `ManualClock` 推进时钟）。
- 两个条件同时成立时按批量触发处理；每次冲刷至多交出一个 `BatchSize` 的批量。

### 高水位背压

- 缓冲内条数达到 `HighWatermark` 时，新的写入**整体拒收**（返回
  `ErrBackpressure`），条目不会进入缓冲，缓冲内容、顺序、下游已收总量
  与逻辑时钟均不变。

### 失败整体回滚

- 一次冲刷若下游报错，整批按原顺序放回缓冲头部并立即停止，返回
  `*FlushError`（含触发类型、批量大小与下游原始错误）；逻辑时钟回滚到
  本次冲刷前，下游已收总量不变（`Sink.Flush` 须原子生效：要么整批成功，
  要么返回错误且无部分效果）。
- 可区分的失败原因：`ErrInvalidConfig`（批量大小 / 高水位 / 延迟阈值
  不合法）、`ErrBackpressure`（高水位拒收）、`ErrEmptyKey`（空键）、
  `*FlushError`（下游报错回滚），均可用 `errors.Is/As` 判定。

### 并发与自检

- `Write`、`Stats`、`Snapshot`、`SelfCheck` 均可并发调用。
- `SelfCheck` 校验不变量：已冲刷数 + 缓冲内数 == 已接受数，且缓冲内
  条目序号恰好接续下游已收前缀（不丢不重、FIFO 可复现）。

### 本地验证（朴素重放核对）

并发写入排空后，用“朴素重放”核对下游结果：被接受的写入按接受顺序
分配序号 `1..N`，下游收到的条目序列必须恰好是 `Seq = 1,2,...,N`
连续递增——多一条、少一条或乱序都会立即失败。

```bash
# 全部单测（含竞态检测），日志打印写入、触发类型、冲刷条目与判定依据
go test -race -v ./buffer/

# 只看并发重放核对用例
go test -race -v -run TestConcurrentWritesReplay ./buffer/
```
