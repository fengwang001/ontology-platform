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

`router` 包实现带会话粘性与排空期的灰度切换路由器，把带会话键的请求粘到某台主机，
并让进入排空的主机只服务已有会话、在到期或会话耗尽后移除。

### 构造与主机生命周期

- `NewRouter(T, D)`：`T` 为会话空闲时长，`D` 为排空期限（int64，均须在 `[1, 1e9]`，否则整体拒绝）。
- `AddHost(id)`：登记主机（id 非空、按字节序比较），登记后为**活跃**态；id 为空或已存在（含已移除）分别报 `ErrEmptyHostID`、`ErrHostExists`。
- `Drain(id, now)`：把活跃主机转为**排空**态并记排空起点 `s = now`。
- **已移除**是终态，其 id 不可再登记，且已移除主机没有任何绑定。

### 绑定有效期与落实（settle）

- 绑定是会话键到主机的记录，带最近访问时刻 `last`；绑定在 `now` 有效当且仅当 `last + T > now`（恰等于时已过期）。
- 落实：对每台排空态主机按 id 升序检查，若它在 `now` 没有任何有效绑定，或 `now >= s + D`，则转为已移除并删除其全部绑定。
- 每次通过时钟检查的 `Route`/`Drain`/`Status`/`Live` 调用都会先推进最大 `now` 再执行落实；`Drain` 在转为排空态后再落实一次（排空瞬间没有有效绑定的主机立即被移除）。

### 路由与新绑定选主规则

- `Route(key, now)`：先落实；若 `key` 的绑定有效，返回其主机（不论活跃或排空）并把 `last` 置为 `now`；否则丢弃该绑定，在**活跃**主机中选有效绑定数最少者（并列取 id 最小），建立 `last = now` 的绑定并返回；没有活跃主机时报 `ErrNoActiveHost`。
- 排空态与已移除的主机不会被选为新绑定的主机；已过期绑定不计入有效绑定数。

### 拒绝顺序与状态不变性

`Route`/`Drain`/`Status`/`Live` 按以下顺序只报第一个错误：

1. 参数非法（`Route` 的 key 为空串；其余的主机不存在）；
2. 时间非法（`now < 0` 或 `now > 1e15`）；
3. 时钟回退（`now` 小于已通过时钟检查的最大 `now`，初值 0）。

以上三类被拒绝的操作不得改变任何状态（含最大 `now`）。通过时钟检查的调用把最大 `now`
推进到 `now` 并执行落实；之后若因状态原因被拒绝（`Route` 无活跃主机、`Drain` 的主机不是活跃态），
落实与最大 `now` 的推进保留，其余不变。

### 并发与可复现性

全部操作由互斥锁串行化，并发调用等价于某个串行顺序；相同操作序列重放得到完全相同的
路由结果、主机状态与会话计数。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照）
go test ./router

# 竞态检测 + 打印每步输入/输出/判定依据
go test -race -v ./router

# 只跑随机对照
go test -run TestModelReplay -v ./router
```
