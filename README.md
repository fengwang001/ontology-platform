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

## SNAT 连接跟踪表（`conntrack` 包）

`conntrack` 实现了一个并发安全的源地址转换（SNAT）出站连接跟踪表：为出站
连接分配外部端口、按连接状态使用不同超时，并且只放行属于已有连接的入站回包。

### 核心模型

- 配置：端口池闭区间 `[lo,hi]`、连接数上限 `N`、半开超时 `Th`、已建立超时
  `Te`、关闭超时 `Tc`；构造时 `lo>hi`、`N<=0` 或任一超时 `<=0` 均返回
  `ErrInvalidConfig`。
- 连接由四元组 `(内部地址, 内部端口, 远端地址, 远端端口)` 唯一标识。
- 事件：出站/入站方向上的 `SYN`、`DATA`、`FIN`、`RST`，每个事件携带逻辑
  时刻 `now`（单调的整数时间戳，非墙上时钟）。

### 端口分配规则

- 外部端口属于“内部端点”（内部地址+端口），而非单条连接：同一内部端点的
  所有连接共用同一个外部端口。
- 新内部端点取端口池中**最小的空闲端口**（最小堆实现）。
- 仅当该内部端点的最后一条连接消失（RST 删除或到期清理）时端口才归还池中，
  因此任意时刻每个外部端口至多属于一个内部端点。
- 新建连接前先检查 `N`：连接数已满返回 `ErrTableFull`；随后才为新端点取端口，
  池空返回 `ErrPortPoolExhausted`。

### 状态迁移与到期

| 事件 | 半开 | 已建立 | 关闭 |
| --- | --- | --- | --- |
| 出站 SYN/DATA | 放行，到期 `now+Th` | 放行，到期 `now+Te` | 放行，到期不变 |
| 入站 SYN/DATA | 放行并转已建立，到期 `now+Te` | 放行，到期 `now+Te` | 放行，到期不变 |
| FIN（任一方向） | 放行并转关闭，到期 `now+Tc` | 放行并转关闭，到期 `now+Tc` | 放行，到期不变 |
| RST（任一方向） | 立即删除连接并按规则释放端口 | 同左 | 同左 |

- 只有出站 `SYN` 能建立新连接，新连接进入半开，到期为 `now+Th`。
- **到期时刻不晚于现在即失效**：`expiry <= now` 即删除，失效连接视为不存在；
  每次事件处理（通过时钟检查后）惰性清理所有失效连接。
- 关闭状态下放行的任何包都不再刷新到期。
- 查询 `Snapshot(now)` 只读、不加钟，返回 `expiry > now` 的连接，结果排序确定。

### 拒绝顺序

被拒绝的操作不会改变任何连接的状态与到期。

- 出站（仅报告第一个命中的原因）：
  1. 时钟回拨（`now` 小于此前接受的最大时刻）→ `ErrClockSkew`
  2. 无对应连接且事件不是 SYN → `ErrNotSynNoConn`
  3. 需新建连接但连接数已满 → `ErrTableFull`
  4. 需为新端点取端口但端口池已空 → `ErrPortPoolExhausted`
- 入站：
  1. 时钟回拨 → `ErrClockSkew`
  2. 外部端口无映射 → `ErrNoMapping`
  3. 有映射但不存在到该远端的连接（含已到期）→ `ErrNoConnectionForRemote`

### 并发与确定性

- 所有出站、入站处理与查询均可并发调用；表内以单个 `sync.RWMutex` 保护，
  保证连接数 `<=N`、每个外部端口最多一个属主。
- 判定只依赖事件自带时刻与表内状态，不依赖调度顺序；相同事件序列重放结果
  完全相同。
- 每次操作通过 `Logger`（默认为标准 logger，可用 `WithLogger` 替换）打印
  输入（`INPUT`）、判定依据（`ALLOCATE`/`CREATE`/`EXPIRE`/`RELEASE`/迁移与
  刷新原因/`REJECT`）与输出（`OUTPUT`）。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./conntrack

# 覆盖率
go test -race -cover ./...

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：同一内部端点访问不同远端共用外部端口、恰在到期时刻的入站包、
关闭后的包不刷新到期、最小空闲端口与释放后重用、表满与端口耗尽、各类拒绝
顺序与拒绝不改状态、RST 删除、并发不变量（`-race`）、确定性重放与日志内容。
