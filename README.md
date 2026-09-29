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

## 全局时间戳分配器（`tso` 包）

主节点切换场景下的批量时间戳分配器，保证新主即使时钟落后、遇到回拨或持久化失败，
也从不发出不大于旧主可能发出值的时间戳。

### 时间戳构成

- 时间戳为 `(物理毫秒, 逻辑计数)`，按字典序比较（先比物理毫秒，相同再比逻辑计数）。
- 每个物理毫秒有 `L` 个逻辑计数，取值 `0..L-1`。
- 物理部分取 `max(本地时钟, 上次物理部分)`，因此时钟回拨不会产生倒退；
  当前毫秒的逻辑计数用尽后，下一批物理部分加一。
- 一次请求的 `n` 个时间戳必须在同一毫秒内连续；本毫秒剩余计数不足时整批移到下一毫秒，绝不拆分。

### 共享存储与上界续写

- 共享存储只保存一条记录 `(任期 term, 上界 upper)`，`upper` 是开区间：
  主节点只允许发出 `物理部分 < upper` 的时间戳。
- 写入是带任期乐观锁的原子写：仅当写入任期不小于存量任期时才接受；
  存量任期更大时返回 `ErrTermSuperseded`。相等任期放行，供同一主节点续写。
- 发放前若 `物理部分 >= 上界`（已越界）或剩余窗口不足 `W/2`
  （判定为 `2*(upper-physical) < W`），先以 `max(当前物理部分, 上界) + W`
  为新上界持久化，再发放。
- 续写时遇到注入的普通持久化故障：目标物理部分仍严格小于已持久化上界则照常发放；
  已越界则本次请求失败（`ErrPersistUnavailable`）。失败的写入不改变存储，
  失败的请求不消耗任何时间戳。
- 续写时若发现任期已被更高主节点超过，立即清空主节点状态降为从节点，本次不发放。

### 接任起点的推导

旧主可能发出的最大物理毫秒是 `upper - 1`（逻辑计数至多 `L-1`）。
新主任期必须严格大于存量任期，随后原子地读后写：

```
start = max(新主本地时钟, 存量 upper)
新上界 = start + W
```

由于先持久化新上界、再在本地就位，且 `start >= upper`，
新主的首个时间戳物理部分即不小于旧主的上界，因此必然大于旧主可能发出的全部时间戳——
即使新主时钟落后旧主数秒（测试覆盖落后五秒）或发生时钟回拨。

### 拒绝原因（可区分的错误）

| 情形 | 错误 |
| --- | --- |
| `L <= 0` 或 `W <= 0` | `ErrInvalidConfig` |
| `n <= 0` 或 `n > L` | `ErrInvalidCount` |
| 向从节点请求时间戳 | `ErrNotLeader` |
| 以不大于存量的任期接任 | `ErrStaleTerm` |
| 续写时任期被超过 | `ErrTermSuperseded` |
| 续写失败且已越界 | `ErrPersistUnavailable` |

### 并发与确定性

- `Allocate` 内部互斥，可被多 goroutine 并发调用；时间戳全局唯一、
  同一主节点内按发放顺序严格递增。
- 物理时钟可通过 `WithClock` 注入，故障通过 `MemStore.InjectFault` 注入。
  相同的时钟、故障注入与请求序列重放，输出完全相同。
- 使用 `WithLogger` 可输出每次接任/发放/续写的输入、输出与判定依据。

### 本地验证

```bash
# 全量（含竞态检测）
go test -race -v ./tso

# 关键用例：新主落后五秒、逻辑用尽跨毫秒、整批移毫秒、
# 续写失败不越界/越界、被超任期降级、时钟回拨、并发唯一递增、确定性重放
go test -race -run 'TestNewLeaderClockBehindFiveSeconds|TestLogicalExhaustion|TestBatchMoves|TestExtensionFailure|TestSupersededTerm|TestClockMovesBackwards|TestConcurrent|TestDeterministic' -v ./tso
```
