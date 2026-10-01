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

## 服务有序终止编排器（`shutdown` 包）

`shutdown` 包实现带宽限期与强杀升级的服务有序终止编排器 `Orchestrator`。
依赖方向约定：**A 依赖 B 表示 A 必须先于 B 停止**（B 是 A 的被依赖者）。

### 接口

- `Register(now, id, graceMs, deps)`：注册服务，给定标识、正整数毫秒宽限期与所依赖的**已注册**服务；依赖不得成环（含自依赖）。
- `Start(now)`：以 `T0=now` 开始关停，只能调用一次。
- `ReportExit(now, id)`：上报服务在 `now` 时刻自行退出。
- `Query(now, id)` / `Snapshot(now)`：查询终止时刻、停止时刻与停止方式（`Exited`/`Killed`）。
- `Events()`：按结算顺序返回全部事件（终止信号下发、自行退出、强杀），用于审计与复现。

### 终止与停止时刻的推导

- **终止时刻**：开始关停时，所有没有存活依赖者的服务在 `T0` 收到终止信号；
  其余服务在其全部依赖者都停止的时刻收到信号，即
  `terminatedAt(s) = max{ stoppedAt(d) | d 是 s 的依赖者 }`，无依赖者时取 `T0`。
- **停止时刻**：服务在宽限期内上报退出，则 `stoppedAt = 上报时刻`，方式为 `Exited`；
  若到 `terminatedAt + graceMs` 仍未退出，则在该时刻被强杀，
  `stoppedAt = terminatedAt + graceMs`，方式为 `Killed`。
  恰在 `terminatedAt + graceMs` 上报的退出视为已被强杀而拒绝（返回 `ErrAlreadyStopped`）。
- 不变量：`stoppedAt - terminatedAt <= graceMs` 恒成立。

### 强杀级联结算规则

任何调用（注册/开始/上报/查询）都先按自己的时刻 `now` 做结算：
把所有满足 `terminatedAt + graceMs <= now` 的服务按 **(到期时刻, 注册序号)** 依次强杀；
每次强杀可能使其被依赖者的全部依赖者都已停止，从而立即下发下游终止信号，
若该下游的强杀时刻同样不超过 `now`，则在同一调用内按时间先后继续级联结算，
直到没有到期强杀为止。结算只依赖调用序列与时刻，不依赖墙上时钟，
因此相同调用序列重放得到完全相同的时刻表。

### 单调时钟与拒绝原因

所有调用共享一个单调时钟：时刻早于此前见过的任一时刻即整体拒绝
（`ErrTimeRegression`）。其余可区分原因：`ErrNonPositiveGrace`、
`ErrDuplicateService`、`ErrUnknownDependency`、`ErrDependencyCycle`、
`ErrRegisterAfterStart`、`ErrAlreadyStarted`、`ErrServiceNotFound`、
`ErrAlreadyStopped`、`ErrNotTerminated`。上报退出按
「时刻回拨 → 服务不存在 → 已停止 → 尚未收到终止信号」的顺序只报第一个。
被拒绝的操作不改变任何服务的状态。所有方法可并发调用，
内部以互斥锁串行化，结果等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含竞态检测）
go test -race ./shutdown/

# 查看输入/输出/判定依据日志
go test -v ./shutdown/
```

测试覆盖：菱形依赖取最晚停止时刻、恰在宽限期末尾上报被视为强杀、
连续两级强杀的时刻级联、一次调用跨过多个到期点的结算顺序、
上游先自行退出而下游被强杀、各类拒绝原因、重放确定性与并发不变量。
