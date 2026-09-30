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

`neighbor` 包实现 RFC 4861 邻居不可达检测（NUD）的简化状态机，解析对端网络地址对应的链路层地址，
在五种状态间迁移并暂存待发包。所有操作并发安全（互斥锁串行化状态变更，回调在锁外执行）。

### 参数

| 参数 | 字段 | 含义 |
| --- | --- | --- |
| R | `ReachableTime` | 可达保持时间，到期转陈旧 |
| Dl | `DelayTime` | 延迟保持时间，到期转探测 |
| T | `RetransTimer` | 地址解析/探测请求重发间隔 |
| K | `MaxAttempts` | 请求最多发送次数（含第 1 次） |
| Q | `QueueLimit` | 每条目暂存包上限，超出丢最旧 |
| - | `MaxEntries` | 条目数上限 |

### 五种状态

- `Incomplete`（未完成）：尚无链路层地址，请求已发出，待发包入队暂存。
- `Reachable`（可达）：链路层地址已知且最近被验证。
- `Stale`（陈旧）：链路层地址可能仍可用但可达性未经验证，不能当作可达。
- `Delay`（延迟）：陈旧条目有发包后短暂等待上层确认的窗口。
- `Probe`（探测）：等待期内无确认，开始主动发送探测请求。

### 到期处理（每个条目每次操作至多处理一个动作；`now >= deadline` 即到期）

- `Reachable` 到期 → `Stale`，清除到期时刻。
- `Delay` 到期 → `Probe`，发第 1 次探测，下次重发时刻 = `now + T`。
- `Incomplete` / `Probe` 重发时刻到期：
  - 已发次数 `< K`：再发一次请求，次数加一，下次重发时刻推后 `T`；
  - 已发次数 `= K`：删除条目；`Incomplete` 队列中全部暂存包计为“不可达丢弃”。

### 发送数据包（`Send`）

- 无条目：新建 `Incomplete`，包入队，发第 1 次请求，下次重发 = `now + T`。
- `Incomplete`：包入队；队列超过 `Q` 时丢最旧的包并计入“溢出丢弃”。
- `Reachable` / `Delay` / `Probe`：直接放出（携带已记录的链路层地址）。
- `Stale`：直接放出并转 `Delay`，到期 = `now + Dl`。

### 应答（`Advertise`，参数：链路层地址、是否为对本方请求的回应 solicited、是否覆盖 override）

- `Incomplete`：记录链路层地址，按入队顺序（FIFO）放出全部暂存包；回应 → `Reachable`（到期 `now + R`），非回应 → `Stale`。
- 其余状态且 `override=true` 或地址与原地址相同：记录地址；回应 → `Reachable`；非回应且地址变化 → `Stale`；非回应且地址相同 → 状态不变。
- `override=false` 且地址不同：`Reachable` → `Stale`（不记录新地址）；其余状态完全忽略。

### 上层确认（`Confirm`）

- 仅 `Delay` / `Probe` → `Reachable`（到期 `now + R`）；其他状态不受影响。

### 拒绝规则（固定优先级，只报第一个；拒绝发生在到期处理之后）

1. 时钟回拨（早于上一次成功操作的时刻）→ `ErrClockBackward`
2. 网络地址为空 → `ErrEmptyAddress`
3. 链路层地址为空（仅应答）→ `ErrEmptyLinkLayer`
4. 对不存在的条目应答或确认 → `ErrNoEntry`
5. 条目数已达 `MaxEntries` 时新建 → `ErrEntryLimit`

被拒绝的操作不改变任何条目、计数或暂存包；但操作携带的到期处理仍然生效。
`Tick(now)` 只推进时刻并做到期处理。

### 语义保证

- 发送、应答、确认、到期处理均可并发调用；内部互斥，回调（放包/发请求）在锁外触发。
- 每个包恰好被放出一次或计为丢弃一次；暂存包放出顺序等于入队顺序。
- 到期处理按地址排序遍历，相同操作序列重放的日志、状态、放出与丢弃结果完全相同。
- `Logger` 打印每次操作的输入、放出/请求输出与判定依据；`Stats()` 返回不可达丢弃与溢出丢弃计数。

### 本地验证

```bash
# 全部用例（16 个，含状态边界、溢出、丢弃、拒绝顺序、并发、确定性重放）
go test ./neighbor -v

# 竞态检测
go test -race ./neighbor

# 全量测试与静态检查
go test ./...
go vet ./...
gofmt -l .
```
