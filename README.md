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

## 会话生命周期管理（`session` 包）

`session.Manager` 以空闲期 `I`、绝对期 `A`、每用户并发上限 `N` 为参数，
管理会话的创建、活动、登出与查询。所有方法均可并发调用，
内部以互斥锁串行化，相同的操作与时钟序列产生完全相同的状态与标识。

### 有效判定与状态优先级

- 会话有效当且仅当 `now < lastActive + I` 且 `now < createdAt + A`，恰到点即失效。
- 活动只对有效会话把 `lastActive` 置为当前时刻（并发活动取各次时刻最大值），不延长绝对期。
- 两种超时同刻成立时报绝对超时。
- 对非有效会话活动或查询时，按「已登出 → 被驱逐 → 绝对超时 → 空闲超时」的优先级报告状态。

### 驱逐规则

- 创建时先排除已失效会话（不占名额、不被误驱逐）。
- 若该用户有效会话数已达 `N`，驱逐最近活动最早者；
  并列取创建更早者，再并列取标识序号小者。被驱逐状态为 `evicted` 而非超时。
- 任意串行化点上每个用户的有效会话数不超过 `N`。

### 错误

- 空用户：`ErrEmptyUser`；会话不存在：`ErrSessionNotFound`；
  `I`/`A`/`N` 非正：`ErrNonPositiveIdle`/`ErrNonPositiveAbsolute`/`ErrNonPositiveMaxPerUser`。
- 对非有效会话活动返回 `*StateError`，携带按上述优先级判定的状态。
- 被拒绝的操作不改变任何状态。

### 本地验证

```bash
# 运行会话包全部测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./session/
```
