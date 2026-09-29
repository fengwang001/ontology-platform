# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双时钟水位维护器（`watermark` 包）

事件时间与处理时间使用两套彼此独立的水位，保证迟到判定只由事件时间决定、
处理时间绝不干扰，且结果始终可复现。

### 推进规则

- **事件时间水位**：`已见最大事件时间 - 允许迟到（allowedLateness）`；尚未见过
  任何事件时为负无穷（`View.EventWatermarkNegInf == true`）。它只由按时接受的
  事件推进，迟到事件不推进任何水位，心跳也绝不触碰它。
- **处理时间水位**：只由 `Heartbeat` 推进，只进不退（等值心跳允许，严格变小即
  回退）。摄入事件时传入的处理时间戳仅写入操作日志，绝不推进该水位。

### 迟到判定规则

- 仅比较 `事件时间` 与 `事件时间水位`：`事件时间 <= 事件时间水位` 即迟到
  （恰好等于水位也算迟到），事件被丢弃、迟到计数加一、不改变任何其他状态；
  `事件时间 > 事件时间水位` 则按时接受、加入已接受视图并按最大事件时间推进水位。
- 处理时间（无论多超前或多落后）与心跳均不参与判定，也不改变事件时间相关状态。
- 整体拒绝（可区分原因，均不改变任何状态）：
  - `negative_allowed_lateness`：构造时允许迟到为负；
  - `empty_event_id`：摄入空标识事件；
  - `heartbeat_regression`：心跳相对当前处理时间水位回退。

### 并发与可复现

- `Ingest` / `Heartbeat` 可并发调用，`Snapshot` / `SelfCheck` 可并发查询；
  单把互斥锁给出操作的全序，两个水位各自单调不减。
- 并发摄入互不相同的按时事件后，视图恰好包含全部事件。
- 每次成功的摄入（含事件时间与处理时间戳）与心跳按加锁总序写入操作日志；
  被拒绝的调用不产生日志。`Replay` 按序重放日志重建出终态完全一致的维护器，
  `SelfCheck` 内部也会执行该重放并核对不变量。

### 本地验证（按序重放核对）

```bash
# 全量单测（含竞态检测），-v 日志逐条打印事件、两个水位、判定与判定依据
go test -race -v ./watermark

# 仅看重放核对用例
go test -race -run TestReplayReproducesState -v ./watermark

# 自检（内部同样做按序重放并比对终态）
go test -run TestConcurrentIngestHeartbeatAndQuery -v ./watermark

go vet ./...
gofmt -l .
```

核对方式：单测先在并发交错下得到终态 `View`，再调用 `watermark.Replay`
按日志顺序重放，断言重放视图与原终态逐项一致（事件时间水位、处理时间水位、
已接受事件集合、迟到计数）。因为判定只依赖事件时间，重放结果与当初的并发
交错无关，任何机器上重复运行都应得到同一结果。

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
