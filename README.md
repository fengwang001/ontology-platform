# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 逻辑复制槽（`replication` 包）

逻辑复制槽维护日志流、解码状态与两个位点，保证崩溃重启后
能重新发出尚未确认的事务，且不重不漏。

### 两个位点

- **确认位点（confirmed LSN）**：消费端已确认的最大提交位点。
  `Confirm(lsn)` 推进确认位点，`lsn` 必须恰好等于某个已发出事务的
  提交位点，且必须严格前进。
- **重启位点（restart LSN）**：`min(确认位点, 所有进行中与已提交未确认事务的起点)`。
  崩溃重启后从重启位点重放保留日志，重新发出已提交未确认的事务。

任意时刻满足 `重启位点 <= 确认位点`；`Positions()` 原子返回两者，
并发读到的位点各自单调递增。

### 日志保留与回收

- 日志记录携带严格递增的 LSN，按事务交错追加（`Append`）。
- 解码器读到提交时发出整个事务，读到中止时丢弃该事务。
- 需保留：进行中事务、已提交但未确认事务的全部日志。
- 回收：早于重启位点的日志立即回收，`RetainedFrom()` 返回当前保留的
  最早序号。

### 错误与拒绝

以下操作被拒绝且不改变日志、回收位置、解码状态或两个位点，
原因可用 `errors.Is` 区分：

- `ErrInvalidRecord`：LSN 未递增、记录类型未知、事务未开启、重复开启。
- `ErrTooManyInProgress`：进行中事务数超过 `NewSlot` 指定的上限。
- `ErrConfirmNotOnBoundary`：确认位点未落在已发出事务的提交位点上。
- `ErrConfirmRegression`：确认位点未严格前进。

### 本地验证

```bash
# 运行复制槽全部测试（含竞态检测，日志打印输入、两个位点、
# 发出的事务与判定依据）
go test -race -v ./replication/

# 指定用例
go test -race -v -run TestRestartReemitsUnconfirmedCommitted ./replication/
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
