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

## 事务发件箱中继（outbox 包）

`outbox` 包实现事务发件箱模式：业务事务提交时写入的消息由中继按提交顺序
投递给下游，投递成功后标记，保证崩溃重启后不丢消息、不多应用。

### 发件箱

- 事务按名字开启：`Begin(name)`，之后 `Write(tx, msg)` 写入消息，
  `Commit(tx)` 提交或 `Abort(tx)` 中止。
- 每次写入分配单调递增的写入序；`Commit` 为事务内全部消息分配同一个
  提交序，消息按 `(提交序, 写入序)` 进入待投队列。
- 被中止事务的消息从不进入待投队列，因此永不投递。
- 拒绝规则（原因可用 `errors.Is` 区分，且被拒绝的操作不改变标识/提交序
  计数器、待投队列或下游状态）：
  - `ErrTxExists`：同名事务已存在；
  - `ErrTxNotFound`：事务不存在；
  - `ErrTxNotActive`：事务已提交或已中止，不可再写入/提交/中止；
  - `ErrInvalidMessage`：消息标识或内容为空；
  - `ErrBacklogExceeded`：提交后待投积压超限，事务保持活跃可重试。

### 投递与标记

- `Relay.RunOnce` 每次取出全部已提交未标记的消息，按 `(提交序, 写入序)`
  逐条处理，每条都是**先投递后标记**（`MarkDelivered`）。
- 下游 `IdempotentSink` 按消息标识幂等去重：同一标识重复投递只应用一次。

### 崩溃恢复

- 崩溃点模拟为“最后一条已投递但未标记”（`SetCrashBeforeLastMark`）。
- 重启后中继重新取出未标记消息并重投；下游按标识去重，因此既不丢消息
  （未标记的重投）也不多应用（重复投递被去重）。
- 存储与中继均可并发调用；并发下下游最终应用序列与按 `(提交序, 写入序)`
  排列的朴素参照（`CommittedOrder`）一致且无重复，同一输入序列反复运行
  输出完全相同。

### 本地验证

```bash
# 运行 outbox 全部测试（含竞态检测），日志打印输入、投递序列与判定依据
go test -race -v ./outbox/
```

覆盖用例：崩溃后重投、中止事务不投递、下游幂等去重、各类非法输入拒绝、
并发下与朴素参照一致、反复运行输出确定。
