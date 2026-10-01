# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 带遗嘱消息的客户端连接登记表（`registry` 包）

`registry.Registry` 维护客户端在线连接、保活判定与遗嘱（Last Will）延迟发布。
所有时间均由调用方以毫秒整数传入，组件自身不读系统时钟，因此相同调用序列可确定性重放。

### 操作

- `Connect(clientID, keepAliveSec K, will|nil, now)`：登记连接，连接本身记一次活动（最近活动时刻 = now）。
- `Active(clientID, now)`：更新最近活动时刻为 now。
- `Disconnect(clientID, normal, now)`：`normal=true` 正常断开（遗嘱作废）；`normal=false` 异常断线。
- `Advance(now)`：仅推进时钟。
- `Published()`：取已发布遗嘱记录快照，记录为 `(客户端, 主题, 载荷, 发布时的 now)`。

### 入口处理次序（每次操作执行自身逻辑前）

1. 校验时钟与参数（见“拒绝规则”），不合法则整体拒绝、不改任何状态。
2. 先按客户端标识升序，逐个判定全部保活超时（同一批按 id 升序断线，分配断线先后序）。
3. 再发布所有计划时刻 `dueAt <= now` 的待发布遗嘱，按 `(dueAt, 断线先后序)` 升序；记录的发布时刻为本次调用的 now。
4. 最后执行操作自身逻辑（含活动/断开的在线检查）。

### 保活规则

- `K = 0`：不检测保活。
- `K > 0`：当 `now - 最近活动时刻` **严格大于** `1500 * K` 毫秒时判为异常断线；
  恰好等于 `1500 * K` 不超时，多 1 毫秒才超时。
- 断线时刻取“发现它的那次调用”的 now，而非理论到期时刻；遗嘱计划时刻 = 断线时刻 + D。

### 遗嘱延迟与发布

- 异常断线且有遗嘱：进入等待发布，`dueAt = 断线 now + DelayMillis D`。
- `D = 0`：在同一次入口处理内（超时判定完成后）立即发布。
- 正常断开、被接管：遗嘱作废，永不发布。
- 每份遗嘱恰好处于“作废 / 等待发布 / 已发布”之一，至多发布一次；
  每个客户端至多一个在线连接、至多一个等待发布的遗嘱。

### 接管与取消

- 同标识 `Connect` 时若旧连接仍在线：旧连接立即替换，旧遗嘱作废。
- 该标识尚在等待发布的遗嘱一并取消。
- 边界：重连恰在遗嘱 `dueAt` 时刻时，入口处理先发布到期遗嘱，接管时它已发布、不可取消。

### 拒绝规则（优先级自上而下）

1. 时钟倒退：`now` 小于此前任一次调用传入的 now（`ErrClockWentBack`）。
2. 客户端标识为空（`ErrEmptyClientID`）。
3. `K < 0`（`ErrNegativeKeepAlive`）。
4. `D < 0`（`ErrNegativeDelay`）。
5. 活动/断开的客户端当前不在线（`ErrClientNotOnline`，含刚在入口处理中被判超时者）。

前四类拒绝在入口处理之前发生，不改变任何状态；第 5 类发生在入口处理之后，
入口处理已产生的断线与发布保留，操作自身无效果。错误统一为 `*registry.CallError`，
以 `Code` 区分原因。

### 并发与确定性

所有操作经同一把互斥锁串行化，并发调用结果等价于某个串行顺序；
发布顺序只依赖 `(dueAt, 断线先后序)`，不依赖 goroutine 调度，序列重放结果完全相同。

### 日志

`New(logger)` 接受 `registry.Logger`（`Logf(format, args...)`）；传 `nil` 使用默认标准日志，
测试中可传入捕获型 logger 打印每次调用的输入、输出与判定依据（超时、计划时刻、发布、接管取消等）。

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
go test ./registry
go test -run TestKeepaliveBoundary ./registry
# 随机调用序列与独立朴素逐步模型对照（每步比对拒绝码、本次发布与完整发布记录）
go test -v -run TestAgainstNaiveModel ./registry

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
