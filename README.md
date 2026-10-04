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

## h2pool：HTTP/2 客户端连接池

`h2pool` 包实现确定性的流分配与优雅关闭管理。所有方法可并发调用，
效果等价于某个串行顺序；相同调用序列重放得到完全相同的选连接结果、
重试名单与状态迁移。

### 构造参数

`NewPool(m0, idleTimeout, maxID, k)`：

- `m0`：每条连接的初始并发流上限，1..1000
- `idleTimeout`：空闲超时（毫秒），1..1e9
- `maxID`：可分配的最大流号，奇数，1..2^31−1
- `k`：连续拒绝阈值，1..100

每条连接记录：状态（Active/Draining/Closed）、`maxConc`（初为 `m0`）、
`nextID`（初为 1，每分配一个流加 2）、活动流表（流号→请求标识）、
`goAwayLast`（初为 `maxID`）、`idleSince`（初为加入时刻）、`refuse`（初为 0）。

### 选连接与并列规则

`Open(now, req)` 的候选为 Active 且活动流数**严格小于** `maxConc` 的连接；
选活动流数最少者，并列取 id 最小者。分配 `nextID` 作为流号后 `nextID += 2`；
若加后大于 `maxID`，连接变为 Draining（流号 `maxID` 仍是可分配的最后一
个）。无候选时报 `ErrNoCapacity`。

### 并发上限收缩

`SetMaxConcurrent(now, id, m)`（1..1000）只改 `maxConc`，已有活动流不被取
消；即使 `m` 小于当前活动流数也成功——此后该连接在活动流数降到严格小
于 `m` 之前不再接收新流。

### GOAWAY 的未处理判定与次序

`GoAway(now, id, lastID)` 按序判定，只报第一个错误：

1. 参数非法：未知 id；`lastID` 非 0 且（为偶数、为负或大于 `maxID`）。
2. 时钟回退：`now` 小于上次被接受调用的 `now`。
3. 状态错误：连接已 Closed。
4. `ErrGoAwayUp`：`lastID` 大于此前已接受的 `goAwayLast`。
5. `ErrGoAwayBeyond`：`lastID` 大于最后一个已分配流号（`nextID−2`，尚
   未分配视为 0）且不等于 `maxID`。

通过后 `goAwayLast=lastID`，Active 连接变为 Draining；流号大于 `lastID`
的活动流被移除，其请求标识按流号升序返回（未处理、可安全重试）；流号
不超过 `lastID` 的流继续。移除后活动流数为 0 则连接变为 Closed。
`GoAway(lastID=maxID)` 不移除任何流，仅使连接排空。

### 拒绝计数与流号耗尽的排空规则

`CloseStream(now, id, stream, kind)`：

- `Done`：`refuse` 清零，不可重试。
- `Refused`：`refuse` 加 1，可重试；`refuse >= k` 且连接为 Active 时变为
  Draining。
- `Reset`：`refuse` 不变，不可重试。

移除后活动流数为 0：Draining 变为 Closed，Active 则令 `idleSince=now`。
`Tick(now)` 把 Active、活动流数为 0 且 `now−idleSince >= idleTimeout`（恰
等即关闭）的连接变为 Closed，返回被关闭 id 的升序列表。

### 时钟与拒绝语义

错误按「参数非法 → 时钟回退 → 状态错误 → ErrGoAwayUp → ErrGoAwayBeyond
→ ErrNoCapacity」的顺序只报第一个；被拒绝的调用不改变任何连接状态、
流表与时钟。连接 id 一经使用（含已 Closed）不可重复加入；在飞请求标识
全局唯一。

### 本地验证

```bash
# 全部单测（含规范工作示例、边界场景与 2000 组随机序列对照朴素模拟）
go test ./h2pool/

# 查看随机对照的输入、输出与判定依据日志
go test -v -run TestRandomAgainstNaiveModel ./h2pool/

# 竞态检测
go test -race ./h2pool/
```
