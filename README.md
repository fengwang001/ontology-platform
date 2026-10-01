# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 变更缓冲（ontology.ChangeBuffer）

`ontology` 包提供带背压的批量冲刷变更缓冲，把写入按批量交给下游（`Sink`），
保证下游收到的条目不丢不重、顺序可复现。

### 两档冲刷触发

- **批量触发（优先）**：缓冲条目数达到 `BatchSize` 立即冲刷队首一批。
- **延迟触发**：最旧条目停留时长达到 `MaxDelay` 触发冲刷（写入时与 `Check`
  自检时评估）。
- 两个条件同时满足时批量触发优先；一次冲刷成功后若仍满足条件会继续冲刷。

### 高水位背压

- `Write` 是原子的：一批写入若使缓冲超过 `HighWater`，整批拒收（`ErrBackpressure`），
  一条都不入缓冲；含空键的写入整批拒绝（`ErrEmptyKey`）。
- 非法配置（`BatchSize < 1`、`HighWater < BatchSize`、`MaxDelay <= 0`、下游为 nil）
  在 `NewChangeBuffer` 直接返回 `ErrInvalidConfig`。
- 所有失败原因均可用 `errors.Is` 区分，且失败不改变缓冲内容、顺序、
  下游已收总量与时钟。

### 失败整体回滚

- 下游 `ApplyBatch` 报错时，整批按原顺序放回缓冲头部并立即停止，
  内部时钟（最旧条目时间戳）回滚到本次冲刷前，返回 `ErrDownstream`。
- 下游契约：`ApplyBatch` 必须原子生效；返回错误即视为整批未生效。

### 并发

- `Write` 可被并发调用，`Stats`/`Snapshot`/`Check` 可并发查询与自检。
- 并发写入后冲刷排空，下游收到的条目恰好等于被接受写入的 FIFO 顺序。

### 本地验证（朴素重放核对）

测试注入假时钟（`Config.Clock`）保证可复现。核对方法：维护一个朴素模型——
每次 `Write` 成功就把键按顺序追加到模型切片，拒收/回滚不改模型；跑完脚本化
操作后，断言下游收到的键序列与模型完全一致（见 `TestNaiveReplay`）。
并发场景（`TestConcurrentWritesFIFO`）核对：下游条目数 == 接受写入数、
每个键恰好出现一次、同一写入者的键保持其发出顺序。

```bash
# 运行全部测试（单测日志打印写入、触发类型、冲刷条目与判定依据）
go test -v ./ontology

# 竞态检测
go test -race -v ./ontology
```

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
