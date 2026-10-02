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

## retrybudget：滑动窗口式重试预算

`retrybudget` 包实现带双窗口、存入封顶与递增定价的重试预算，所有时刻为 `int64`。

### 构造参数与合法性

`New(Wd, Wc, D, C, R, Mx, Cm)`：

- `Wd`、`Wc`：存入 / 取出窗口，范围 `[1, 1e9]`
- `D`：每个请求的存入额度，`C`：基础重试额度，范围 `[1, 1e6]`
- `R`：保底额度，范围 `[0, 1e9]`；`Mx`：定价倍数上限，范围 `[1, 10]`；`Cm`：存入计数封顶，范围 `[1, 1e6]`
- 任一参数越界，整体以 `ErrInvalidConfig` 拒绝

### 事件有效期与余额公式

- `Request(now)` 记一笔存入事件（时刻 `t`）；存入事件对 `now` 有效当且仅当 `t+Wd > now`（恰等于时已失效）
- `TryRetry(now)` 被允许时记一笔取出事件（时刻 `t`，并冻结本次成本）；取出事件对 `now` 有效当且仅当 `t+Wc > now`
- 余额：`Balance(now) = R + D×min(Cm, 有效存入事件数) − Σ(有效取出事件的冻结成本)`，可以为负

### 递增定价、冻结与允许判定

- 设此刻有效取出事件数为 `k`，本次重试成本 `cost = C × min(Mx, 1+k)`，随近期重试数逐级递增并以 `Mx` 封顶
- `Balance(now) >= cost` 则允许并记账，成本在记账时冻结，之后不因 `k` 下降而重新定价；否则不记账并返回拒绝（被拒绝的重试仍是被接受的操作，会推进最大 `now`）
- 拒绝原因可区分并按序只报第一个：`ErrInvalidTime`（`now < 0` 或 `now > 1e15`），`ErrClockRegression`（`now` 小于已被接受操作见过的最大 `now`，初值 0）；被拒绝的操作不改变账本与最大 `now`

### 并发与摊还清理

- 三个方法均可并发调用，互斥锁保证结果等价于某个串行顺序；相同操作序列重放得到完全相同的返回值与余额序列
- 由于被接受操作的时刻单调不减，两条账本均为队列：失效事件只从队首弹出，每条事件至多被考察、清理一次，清理摊还 `O(1)`
- 有效存入数与冻结成本和均增量维护，`Balance` / `TryRetry` 不重新扫描账本；非导出计数器 `cleanedEvents` / `examinedEvents` 记录累计清理与考察数，测试证明清理总数不超过登记事件总数、考察总数不超过操作数的常数倍

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素逐事件扫描对照、摊还清理证明）
go test ./retrybudget/

# 打印每步输入、输出与判定依据（k、cost、余额）
go test ./retrybudget/ -run TestRandomSequencesAgainstNaive -v

# 竞态检测
go test -race ./retrybudget/
```
