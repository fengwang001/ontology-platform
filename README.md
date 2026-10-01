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

## 事务半消息

`halfmessage` 包提供可重放的事务半消息日志，时间戳由调用方传入，不读取系统时钟。

### 状态迁移

- 发送后进入 `Pending`：消费者不可见，也不占用日志位点。
- `Commit` 仅允许 `Pending` 调用，成功后进入 `Committed`，并在提交瞬间追加到日志末尾。
- `Rollback` 仅允许 `Pending` 调用，成功后进入 `RolledBack`，显式回滚原因为 `RollbackExplicit`。
- 回查回调返回 `CheckCommit` 或 `CheckRollback` 时，立即完成对应终态迁移。
- 第 `M` 次回调仍返回 `CheckUnknown` 时立即回滚，原因为 `RollbackChecksExhausted`。
- 终态事务标识继续保留；重复发送返回 `ErrDuplicateTransaction`，不会复用或覆盖原事务。

### 位点分配

- 日志位点从 `0` 开始，只在提交时分配。
- 位点顺序等于实际提交获得内部锁的先后顺序，不等于发送顺序。
- 日志仅包含已提交消息，位点连续且无空洞。
- 回查回调内可以调用提交或回滚；如果回调返回前消息已进入终态，回调返回值会被忽略。

### 回查时刻

- 构造参数要求 `F >= 0`、`I >= 1`、`M >= 1`。
- 首次回查时刻为创建时刻 `+ F`，即 `Tick(createdAt+F)` 可首次处理。
- 后续等待时刻为“上一次实际调用回调时的 `now` + `I`”，不是按原定调度点累加。
- 每次 `Tick` 在开始时按创建顺序快照到点的待定消息；推进期间新发送的消息不参与本次推进。
- 每条消息在一次 `Tick` 中至多回调一次；即使一次推进跨越多个间隔，也只回调一次。
- 处理到某条消息时若它已由其他操作进入终态，则跳过且不增加回查次数。
- 回调在内部状态锁之外执行，因此回调内提交、回滚或发送不会自锁；回调返回后重新读取状态再决定结果。

### 并发与拒绝原因

所有操作都可并发调用。状态、日志和计数由互斥保护，外部观察结果等价于某个合法串行顺序。所有带 `now` 的调用共享单调时钟；小于此前任一次 `now` 的调用返回 `ErrClockMovedBack`，且不改变状态。

可区分错误如下：

- `ErrUnknownTransaction`：未知事务标识。
- `ErrDuplicateTransaction`：事务标识重复发送，包括终态后再次发送。
- `ErrAlreadyCommitted`：对已提交事务再次提交或回滚。
- `ErrAlreadyRolledBack`：对已回滚事务再次提交或回滚。
- `ErrClockMovedBack`：调用方时钟倒退。
- `ErrInvalidConfig`：配置不合法或未提供回查回调。

### 本地验证

```bash
# 详细测试会打印每次输入、输出、回调次数与判定依据
go test ./halfmessage -v

# 全量验证与竞态检测
go test -race -v ./...

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试内包含一个按相同规则逐步执行的朴素模拟器，对状态、位点、回查次数和终态逐步对照，确保相同调用序列与回调返回序列重放结果一致。
