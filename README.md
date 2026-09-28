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

## 事件时间去重窗口（`dedup` 包）

`dedup.Deduper` 按事件标识（`Event.ID`）对带事件时间（`Event.Time`，区别于处理时间）的事件去重，去重记忆由事件时间水位线驱动过期。

### 规则

- **水位线**：取所有已处理事件时间的最大值，单调不降；乱序、相等、迟到事件都不会使其回退。
- **去重记忆**：每个标识只在被判为「新事件」时，记录**首现那条事件**的时间 `remembered_at` 及过期点 `expires_at = remembered_at + TTL`。
- **过期与清除**：处理每条事件时先把水位线推进到候选值，凡是 `expires_at <= watermark` 的记忆**立即清除**（边界相等即过期）。清除与判定在同一次调用内完成，因此结果中的记忆条数立刻反映过期。
- **重复判定**：事件到达时，若其标识在记忆中且按当前水位线未过期 → 重复：丢弃、`duplicates` 加一，**不刷新记忆**（过期点始终锚定首现时间）；否则为新事件：输出、`emitted` 加一并写入记忆。
- **迟到事件不丢弃**：事件时间早于水位线的事件照常参与新/重判定与输出（`Result.Late=true`）。若迟到新事件的过期点已不晚于水位线，事件仍输出，但记忆写入即清除、不占用条数，避免错误压制后续同标识事件。
- **拒绝（状态零变更）**：以下情况返回携带可区分 `RejectReason` 的 `*RejectError`，且**不改变**水位线、记忆、重复计数或已输出事件，`Process` 返回零值结果：
  - `invalid_config`：`TTL <= 0` 或 `MaxEntries <= 0`（构造时拒绝）；
  - `empty_id`：事件标识为空或纯空白；
  - `memory_limit_exceeded`：按候选水位线清除过期记忆后，接受该新事件会使记忆条数超过 `MaxEntries`。
- **并发与一致性**：`Process` 的「推进水位线 → 过期清除 → 判定 → 计数」在同一把写锁内原子完成；`Snapshot()` 在读锁内拷贝全部字段，并发读者拿到的是逐字段一致的状态，不会看到中间态。
- **确定性**：判定只依赖输入序列与单调水位线，不依赖墙钟时间；同一输入序列反复计算得到完全相同的逐事件结果与最终快照。

### 日志

每条事件打印输入（`id`、`event_time`）、判定类别（`dedup new` / `dedup duplicate` / `dedup reject`）与判定依据（`basis`、`watermark`、`remembered_at`、`expires_at`、`late`；拒绝时打印 `reason`）。默认使用 `slog.Default()`，可通过 `Config.Logger` 注入。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细输出（去重器支持并发读写）
go test -race -v ./dedup/

# 仅去重包覆盖率
go test -coverprofile=coverage.out ./dedup/
go tool cover -html=coverage.out

gofmt -l dedup/
go vet ./dedup/
```

测试覆盖：恰好落在过期边界（`expires_at == watermark`）的立即清除、边界前一纳秒仍判重、重复不刷新记忆、迟到事件不丢弃（含「写入即过期」）、水位线单调、三类可区分拒绝原因及其状态零变更、同序列确定性，以及并发读写下的字段一致与计数守恒。
