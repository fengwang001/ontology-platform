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

## 多流两级流量控制记账器（`flowcontrol`）

`flowcontrol` 包实现多流发送端的两级（连接 + 流）流量控制记账，位于
[flowcontrol/controller.go](flowcontrol/controller.go)。

### 构造

```go
c, err := flowcontrol.New(connWindow, initWindow, maxFrame)
// 可选日志：flowcontrol.New(connWindow, initWindow, maxFrame,
//           flowcontrol.WithLogger(logger)) // Logger 接口：Printf(string, ...any)
```

- 参数依次为连接窗口初值、初始窗口 `I`、帧长上限 `F`。
- 所有窗口上限均为 `MaxWindow = 2^31-1`。
- 连接窗口或 `I` 为负或超过上限、`F <= 0` 时拒绝构造（状态自然为空）。

### 两级扣减

- `Send(id, n)`：`n` 必须为正且 `n <= F`；仅当**连接窗口与该流窗口都够**
  （`n <= 两者当前值`，流窗口为负视为不够）时才放行。
- 放行是**全有或全无**的单次原子操作：连接窗口与流窗口同时扣减 `n`；
  任一条件不满足则拒绝，两个窗口都不变。
- 发送拒绝按此顺序只报第一个：流不存在 → 流已关闭 → `n` 非正 →
  `n > F` → `n` 超过连接窗口 → `n` 超过流窗口。

### 开流与关流

- `OpenStream(id)` 使用调用方给定的**未用过的正整数**编号；窗口初值取当时的 `I`。
- 编号非正、或编号已用过（**关闭后也不可复用**）均拒绝。
- `CloseStream(id)` 关闭流；其未用窗口**不回补**连接窗口。

### 窗口增量

- `Increment(0, delta)` 增加连接窗口；`Increment(id, delta)` 增加某流窗口。
- `delta` 必须为正；加和后超过 `MaxWindow` 则拒绝且不改任何状态
  （连接超限报 `ErrConnOverflow`，流超限报 `ErrStreamOverflow`，溢出检查用
  `delta > MaxWindow-window` 比较，避免加法本身溢出）。
- 增量拒绝顺序：流不存在 → 流已关闭 → 增量非正 → 连接窗口溢出 → 流窗口溢出。

### 初始窗口追溯调整

- `AdjustInitWindow(I2)`：差值 `d = I2 - I`（可为负），对**每个未关闭流**
  的窗口追溯加上 `d`；已关闭流不动，连接窗口不变，此后新流取 `I2`。
- 先做整体预检：`I2` 为负或超限即拒绝；**任一未关闭流**调整后超过上限，
  则所有流（含 `I` 本身）都不修改（`ErrStreamOverflow`）。
- 下调可使流窗口变负；负窗口下不可发送（归入“超过流窗口”），
  直到后续增量把窗口补正为止。

### 并发与确定性

- 发送、增量、调整、开/关流与查询全部可并发调用；记账器内部用单把锁
  串行化所有变更与查询，保证：
  - 任意时刻每个窗口都等于按其历史（初值、增量、已放行字节、调整差值）重算之值；
  - 并发发送的放行总量绝不超过连接窗口，各流不超过其流窗口；
  - 相同调用序列重放，返回结果序列完全相同。
- 所有调用（含被拒绝者）都会通过 `Logger` 打印**输入、输出与判定依据**，
  默认不输出；日志在临界区内按判定顺序生成并带序号。

### 本地验证

```bash
# 全量测试（竞态检测）
go test -race -v ./flowcontrol/

# 重复运行以压测并发记账
go test -race -count=5 ./flowcontrol/

# 全仓测试 / 检查
go test ./...
go vet ./...
gofmt -l .
```

重点用例见 [flowcontrol/controller_test.go](flowcontrol/controller_test.go)：
初始窗口下调致流窗口为负、增量补正后恰好可发、调整致某流溢出时整体不改、
连接窗口先耗尽、流窗口先耗尽、关闭流不回补且编号不复用、
拒绝顺序、并发放行总量上限，以及相同序列确定性重放与日志内容断言。
