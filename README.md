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

## 邻居缓存状态机（`neighbor` 包）

`neighbor.Cache` 实现了一个 IPv6 NDP 风格的邻居不可达检测状态机，用网络层地址解析链路层地址，并在解析期间暂存待发包。时间由调用方注入（`neighbor.Time`），因此相同操作序列重放结果完全相同；`Send` / `Advertisement` / `Confirm` / `Tick` 均可并发调用（内部互斥串行化）。

参数：可达时间 `R`（`ReachableTime`）、延迟时间 `Dl`（`DelayTime`）、重发间隔 `T`（`Retransmit`）、最多发送次数 `K`（`MaxSends`）、每条目暂存上限 `Q`（`QueueLimit`），另含条目数上限 `MaxEntries`。

### 五种状态

- `INCOMPLETE`：地址解析中，包暂存；按 `T` 重发请求。
- `REACHABLE`：已知链路层地址且近期可达；到期时刻为进入时刻加 `R`。
- `STALE`：地址已知但可达性过期，不被当作可达；不再计时。
- `DELAY`：陈旧后有包发出，等待上层确认；到期时刻为发包时刻加 `Dl`。
- `PROBE`：延迟到期后单播探测；按 `T` 重发。

### 到期处理（每次操作先执行，也可用 `Tick` 单独推进）

现在不早于到期时刻（`now >= deadline`，边界即到期）；每次调用每个条目至多处理一个动作，条目按地址升序处理：

- `REACHABLE` 到期 → `STALE`。
- `DELAY` 到期 → `PROBE`，发第 1 次探测，下次重发时刻 = 现在 + `T`。
- `INCOMPLETE` / `PROBE` 到期：已发次数 `< K` 时再发一次，下次重发时刻推后 `T`；否则删除条目。`INCOMPLETE` 删除时其全部暂存包计为“不可达丢弃”（`DroppedUnreachable`）。

### 操作规则

- `Send(now, addr, pkt)`：
  - 无条目 → 新建 `INCOMPLETE`，暂存该包，发第 1 次请求，下次重发 = 现在 + `T`；
  - `INCOMPLETE` → 暂存；队列达到 `Q` 再入队时丢弃最旧包并计入 `DroppedOverflow`；
  - `REACHABLE` / `DELAY` / `PROBE` → 直接放出；
  - `STALE` → 放出并转 `DELAY`（到期 = 现在 + `Dl`）。
- `Advertisement(now, addr, link, solicited, override)`（应答）：
  - `INCOMPLETE`：记录链路层地址，按入队顺序（FIFO）放出全部暂存包；对本方请求的回应（`solicited`）→ `REACHABLE`（到期 = 现在 + `R`），否则 → `STALE`；
  - 其余状态：`override=true` 或地址与已记录地址相同时记录地址，回应 → `REACHABLE`，非回应且地址变化 → `STALE`，否则状态不变；
  - `override=false` 且地址不同：`REACHABLE` → `STALE`（不记录地址），其余状态忽略且不记录。
- `Confirm(now, addr)`：仅使 `DELAY` / `PROBE` 转 `REACHABLE`，其他状态不变。
- `Tick(now)`：只做到期处理并推进时钟。

### 拒绝顺序（只报第一个错误）

拒绝发生在到期处理之后；除已完成的到期处理外，被拒绝的操作不改变任何条目、计数或暂存包。顺序固定：

1. 时钟回拨（`now` 早于上次操作时间，`ErrClockMovedBackward`）；
2. 对端地址为空（`ErrEmptyAddr`）；
3. 链路层地址为空（`ErrEmptyLinkAddr`）；
4. 对不存在的条目应答或确认（`ErrNoEntry`）；
5. 条目数已达上限时新建（`ErrCacheFull`）。

### 结果与日志

每个操作返回 `Result`：`Requests`（需发出的请求/探测，含到期触发的）、`Delivered`（放出的包，FIFO）、`DroppedUnreachable`、`DroppedOverflow`、`Expired`（到期迁移明细）。传入 `*log.Logger` 后日志逐条打印 `[input]`、`[output]` 与 `[basis: …]` 判定依据。

### 本地验证

```bash
# 全部状态机测试
go test -v ./neighbor

# 竞态检测 + 多次重放确定性
go test -race -count=3 ./neighbor

# 全量测试 / vet / 格式
go test -race ./...
go vet ./...
gofmt -l .
```

关键用例见 `neighbor/cache_test.go` 与 `neighbor/cache_more_test.go`：未完成回应与非回应的不同去向、可达到期恰在边界转陈旧、延迟到期转探测、未完成耗尽后删除并丢弃暂存包、不覆盖且地址不同时的分支、暂存溢出丢最旧、拒绝优先级、FIFO 恰好一次、并发安全与确定性重放。
