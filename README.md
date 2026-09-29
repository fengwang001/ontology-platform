# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分区副本同步（`replica` 包）

`replica` 包实现单个分区的同步副本集（ISR）维护与高水位（HWM）推进，
协调领导者写入与跟随者拉取，并周期性剔除落后副本。

### 核心规则

- 位点：每个副本持有日志结束位点 `end`（已拥有日志 `[0, end)`）与
  “追上时间” `caughtUpAt`（最近一次与领导者位点齐平的时间）。
- 写入（`Append`）：仅领导者调用，推进领导者 `end`，随后重算高水位。
- 拉取（`Fetch`）：跟随者以已拥有位点更新进度；位点必须单调且不超过
  领导者。与领导者位点齐平时刷新 `caughtUpAt`；被移出或新注册的副本
  追到当前高水位（与领导者齐平）时重新加入同步副本集。
- 周期检查（`Sweep`）：落后时长 `now - caughtUpAt` **严格大于**容忍
  阈值 `tolerance` 才移出（恰好相等保留）；领导者永不移出。
- 高水位：`hwm = min(同步副本集内全部副本（含领导者）的 end)`，
  **只进不退**，且永不超过同步副本集内任一副本的进度；领导者始终在
  同步副本集中。
- 时间由调用方显式传入且全局单调（`Append`/`Fetch`/`Sweep` 共用逻辑
  时钟），行为确定可复现；时钟回退被整体拒绝。

### 错误类别（整体拒绝，失败不留痕）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | 空副本名、重复注册、负写入长度、位点溢出、零值时间、负容忍时长、领导者执行拉取 |
| `ErrUnknownReplica` | 拉取引用未注册副本 |
| `ErrInvalidOffset` | 负位点、超过领导者位点、相对自身进度回退 |
| `ErrClockRollback` | 操作时间早于系统已观察到的最新时间 |

任一被拒操作都在校验通过前返回，不改变高水位、同步副本集或任何副本进度。

### 并发与日志

所有方法可被多个执行体并发调用（内部 `sync.RWMutex`，校验+变更+高水位
重算原子生效）。`WithLogWriter` 可把每步输入、同步副本集与判定依据写入
指定 `io.Writer`（默认 `os.Stderr`）。

### 本地验证

```bash
# 全部用例（场景、不变量、并发、朴素参照差分）
go test ./...

# 竞态检测 + 详细日志，观察每步输入、同步副本集与判定依据
go test -race -v ./replica

# 只看随机操作流与朴素参照一致性
go test -run TestMatchesNaiveReference -v ./replica
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
