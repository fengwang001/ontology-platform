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

## 复杂事件模式匹配（`cep` 包）

`cep` 包在按键（key）分区的事件流上做先后事件配对：查找先出现的
`FirstType` 事件与之后出现的 `SecondType` 事件组成的匹配对。

### 窗口条件

- 匹配只发生在**同一个键**的事件之间；每个键的事件时间单调不减。
- 时间窗为闭区间：`0 <= Second.Time - First.Time <= Window`。
- 时间差恰好等于 `Window` 也算匹配；超过 `Window` 立即过期，不再配对。

### 两种连续模式（`Config.Mode`）

- `cep.Strict`（严格连续）：后事件必须是同键中紧挨着先事件的下一个事件；
  同键中间夹任何其他类型的事件都会打断匹配。**其他键**的事件不打断严格连续，
  因为每个键各自维护独立的“上一个事件”。
- `cep.Relaxed`（宽松连续）：先、后事件之间允许夹任意事件。每个先事件至多配
  一个后事件（配对后即移出待匹配队列），后事件按先进先出（FIFO）与最早未匹配的
  先事件配对，因此一个后事件也至多消费一个先事件。

### 配对与并发规则

- `ProcessBatch` 以整批为单位原子生效：批内任何事件被拒绝时，待匹配队列、
  每个键的上一个事件以及已输出配对都保持不变。
- 拒绝原因通过独立的哨兵错误返回，可用 `errors.Is` 区分：
  `ErrInvalidWindow`、`ErrInvalidMaxPending`、`ErrInvalidMode`、
  `ErrEmptyFirstType`、`ErrEmptySecondType`、`ErrTypeConflict`、
  `ErrEmptyKey`、`ErrEmptyType`、`ErrTimeRegression`、`ErrQueueFull`
  （宽松模式下单个键的待匹配队列达到 `MaxPending` 即拒绝整批）。
- `Matcher` 内部用读写锁保护；`Matches` / `Pending` / `LastEvent`
  返回的都是拷贝，可在写入时并发读取且逐字段一致。配对严格按批内顺序输出，
  同一输入序列反复计算结果完全相同。
- 每次处理会通过 `Config.Logger`（默认 stderr）打印输入事件、配对结果与
  判定依据（入队、配对的 `delta/window`、过期、不匹配原因、拒绝原因）。

### 本地验证

```bash
# 只跑 cep 包（带竞态检测与详细日志）
go test -race -v ./cep

# 全量测试 + 覆盖率
go test -race -cover ./...

# 格式化与静态检查
gofmt -l .
go vet ./...
```
