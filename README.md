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

## 灰度切换路由器（`router` 包）

带会话粘性与排空期的灰度切换路由器，把带会话键的请求粘到某台主机。
以 `NewRouter(T, D)` 构造：`T` 为会话空闲时长，`D` 为排空期限（均为
int64，须在 1 到 10^9，否则整体拒绝并返回 `ErrInvalidConfig`）。

### 绑定有效期与落实（settle）

- 绑定是会话键到主机的记录，带最近访问时刻 `last`；绑定在 `now` 有效
  当且仅当 `last + T > now`（恰等于 `last + T` 时已过期）。
- 落实：对每台排空态主机按 id 升序检查，若它在 `now` 没有任何有效
  绑定，或 `now >= s + D`（`s` 为排空起点），则转为已移除并删除其
  全部绑定。`Route`、`Drain`、`Status`、`Live` 通过时钟检查后都会
  先落实。

### 排空与移除

- `Drain(id, now)` 先落实，再要求主机为活跃态，转为排空态并记
  `s = now`，然后再落实一次——排空瞬间没有有效绑定的主机立即被移除。
- 排空态主机只服务已有有效绑定（`Route` 命中时刷新 `last`，可能因此
  把移除推迟到期限 `s + D`），不会被选为新绑定的主机。
- 已移除是终态：没有任何绑定，其 id 不可再登记（`AddHost` 报
  `ErrHostExists`）。

### 新绑定选主规则

`Route(key, now)` 中绑定失效（不存在或已过期）时，丢弃旧绑定，在
活跃主机中选有效绑定数最少者，并列取 id 最小者（id 按字节序比较），
建立 `last = now` 的绑定；没有活跃主机时报 `ErrNoActiveHost`，但
落实与最大 `now` 的推进保留。

### 错误与拒绝语义

- `AddHost` 拒绝原因可区分：id 为空（`ErrEmptyHostID`）、id 已存在
  含已移除（`ErrHostExists`）。
- `Route`、`Drain`、`Status`、`Live` 按顺序只报第一个错误：参数非法
  （`ErrEmptyKey` / `ErrHostNotFound`）→ 时间非法（`now` 不在
  `[0, 1e15]`，`ErrInvalidTime`）→ 时钟回退（`now` 小于已通过时钟
  检查的最大 `now`，初值 0，`ErrClockRollback`）。这三类拒绝不改变
  任何状态（含最大 `now`）。
- 通过时钟检查后若因状态原因被拒绝（`ErrNoActiveHost`、
  `ErrHostNotActive`），落实与最大 `now` 的推进保留，其余不变。

### 并发与可复现性

全部操作互斥执行，并发调用等价于某个串行顺序；相同的操作序列重放
得到完全相同的路由结果、主机状态与会话计数。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作序列与朴素模拟的对照）
go test ./router/

# 带竞态检测与逐步日志（日志含每步输入、输出与判定依据）
go test -race -v ./router/
```
