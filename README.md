# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## h2pool：HTTP/2 客户端连接池与优雅关闭管理器

`h2pool` 包在多条 HTTP/2 连接间按并发流上限分配请求，并管理连接的
`Active → Draining → Closed` 优雅关闭迁移。所有方法可并发调用，效果等价于
某个串行顺序；相同调用序列重放得到完全相同的选连接结果、重试名单与状态迁移。

### 构造参数

`NewPool(m0, idleTimeout, maxID, K)`：

- `m0`：初始并发流上限，1 到 1000；
- `idleTimeout`：空闲超时（毫秒），1 到 10^9；
- `maxID`：最大流号，奇数，1 到 2^31−1；
- `K`：连续拒绝阈值，1 到 100。

每条连接维护：状态、`maxConc`（初为 `m0`）、`nextID`（初为 1，每分配一个加 2）、
活动流表（流号→请求标识）、`goAwayLast`（初为 `maxID`）、`idleSince`（初为加入
时刻）、连续拒绝数 `refuse`（初为 0）。

### 选连接与并列规则（Open）

- 候选：状态为 `Active` 且活动流数**严格小于** `maxConc` 的连接；
- 选活动流数最少者；并列取 id 最小者；
- 分配 `nextID` 作为流号后 `nextID += 2`；若加后大于 `maxID`，连接变为
  `Draining`（流号 `maxID` 是可分配的最后一个，本次分配仍然有效）；
- 无候选返回 `ErrNoCapacity`；在飞请求标识不可重复（`ErrDupReq`）。

### 并发上限收缩语义（SetMaxConcurrent）

只修改 `maxConc`，已有活动流不被取消；即使新上限小于当前活动流数也成功。
此后仅当活动流数降到严格小于新上限时，该连接才重新成为候选。

### GOAWAY 的未处理判定与次序（GoAway）

按序判定，只报第一个错误：

1. `lastID > goAwayLast` → `ErrGoAwayUp`；
2. `lastID > nextID−2`（最后一个已分配流号，尚未分配视为 0）且
   `lastID != maxID` → `ErrGoAwayBeyond`（`lastID == maxID` 豁免，可用来
   不移除任何流地排空连接）。

通过后：`goAwayLast = lastID`，`Active` 连接变为 `Draining`；流号大于
`lastID` 的活动流被移除，其请求标识按流号升序返回（未处理、可安全重试）；
流号小于等于 `lastID` 的流继续；移除后活动流数为 0 则连接变为 `Closed`。

### 拒绝计数与流号耗尽的排空规则（CloseStream）

- `Done`：`refuse` 清零，不可重试；
- `Refused`：`refuse` 加 1，可重试；`refuse >= K` 且连接为 `Active` 时变为
  `Draining`；
- `Reset`：不改 `refuse`，不可重试；
- 移除后活动流数为 0：`Draining` → `Closed`，`Active` → 记录 `idleSince=now`；
- 流号耗尽（分配 `maxID` 后）连接自动进入 `Draining`，排空后关闭。

### 空闲超时（Tick）

对 `Active` 且活动流数为 0 且 `now − idleSince ≥ idleTimeout` 的连接变为
`Closed`（恰等即关闭），返回被关闭 id 的升序列表。

### 错误与拒绝语义

参数非法（`ErrUnknownConn`、`ErrUnknownStream`、`ErrBadLastID`、
`ErrBadMaxConc`、`ErrBadNow`、`ErrDupReq`、`ErrBadConfig`、`ErrDupConn`、
`ErrBadKind`）→ 时钟回退（`ErrClockBackwards`）→ 状态错误
（`ErrConnClosed`）→ `ErrGoAwayUp` / `ErrGoAwayBeyond` → `ErrNoCapacity`，
按此顺序只报第一个；被拒绝的调用不改变任何连接状态、流表与时钟。

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

# h2pool：规格示例与边界用例
go test -v ./h2pool -run 'TestWorkedExample|TestStreamIDExhaustion|TestGoAwayMaxID|TestRefuseThreshold|TestTickExactTimeout'

# h2pool：2000 组随机调用序列与朴素模拟对照（-v 打印每次调用的输入、输出与判定依据）
go test -v ./h2pool -run TestRandomAgainstNaive

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
