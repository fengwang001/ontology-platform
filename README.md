# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## SNAT 连接跟踪表（`conntrack` 包）

`conntrack` 实现源地址转换（SNAT）的连接跟踪：为出站连接分配外部端口、
按连接状态维护不同超时，并且只放行属于合法连接的入站回包。所有事件均带
逻辑时刻（`int64`，单位由调用方约定，例如毫秒），表的行为只依赖输入事件，
相同事件序列重放结果完全相同。

### 数据模型

- 连接由四元组标识：内部地址与端口（`Src`）+ 远端地址与端口（`Dst`）。
- 外部端口池为闭区间 `[Lo, Hi]`；活动连接数上限 `N`。
- 状态超时：半开 `Th`、已建立 `Te`、关闭 `Tc`。
- 构造非法即拒绝：`Lo > Hi`、`N <= 0` 或任一超时 `<= 0`。

### 状态迁移与到期规则

| 当前状态 | 事件 | 迁移 / 动作 | 新到期 |
| --- | --- | --- | --- |
| （不存在） | 出站 SYN | 新建，进入半开 | `now + Th` |
| （不存在） | 其他任何事件 | 拒绝（出站）/ 拒绝（入站） | — |
| 半开 | 放行的出站 SYN/DATA | 停留半开，刷新 | `now + Th` |
| 半开 | 入站 SYN 或 DATA | 转为已建立 | `now + Te` |
| 半开 / 已建立 | 任意方向 FIN | 转为关闭 | `now + Tc` |
| 已建立 | 任意方向放行的 SYN/DATA | 停留已建立，刷新 | `now + Te` |
| 关闭 | 放行的任何报文 | 停留关闭 | 不变 |
| 任意状态 | 任意方向 RST | 立即删除连接 | — |

- 到期判定：`expireAt <= now` 即失效，失效连接在任何检查中视为不存在；
 因此“恰在到期时刻”的入站包会被拒绝，`now = expireAt - 1` 仍存活。
- 失效条目的回收是惰性的：在下一个被接受的事件处理前统一清扫，
 被拒绝的操作不做清扫、不改变任何连接的状态与到期。
- 时钟单调：会话记录已见到的最大事件时刻，`now` 回拨直接拒绝。

### 端口分配规则

- 同一内部端点（地址+端口）的所有连接共用同一个外部端口；
  只要该端点还有一条存活连接，端口就保持占用。
- 新内部端点取 `[Lo, Hi]` 中数值最小的空闲端口；池空则拒绝。
- 端点最后一条连接消失（RST 立即删除或到期清扫）时，端口才释放，
  释放后可再次作为最小空闲端口被分配。
- 任意时刻每个外部端口至多属于一个内部端点；活动连接数不超过 `N`。
- 新建时的拒绝顺序（出站）：时钟回拨 → 无对应连接且非 SYN →
  连接表满（`N`）→ 端口池已空。
- 入站拒绝顺序：时钟回拨 → 外部端口无映射（`ErrNoMapping`）→
  有映射但无来自该远端端点的连接（`ErrNoConnection`）。

### 并发与日志

- `ProcessOutbound`、`ProcessInbound`、`Lookup`、`Stats` 均可并发调用，
  内部以互斥锁保证原子性与上述不变量。
- `Config.Logger`（实现 `Printf`）会记录每个事件的输入、判定依据
  （端口分配、状态迁移、到期刷新、拒绝原因、清扫与端口释放）和输出。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 查看事件输入/输出/判定依据日志
go test -race -v -run TestEventLogDemo ./conntrack

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

最小用法示例见 `conntrack/conntrack_test.go`。

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
