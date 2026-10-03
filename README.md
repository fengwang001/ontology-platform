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

## 混合关键级运行时模式监控器（`mcsched`）

单核固定优先级调度下的运行时监控器，位于 `mcsched` 包。通过 `New()` 创建，
用 `AddTask` 注册任务（最多 16 个），用 `SetDemand(id, k, c)` 覆盖第 `k`
个作业（`k` 自 0 起，释放时刻 `φ+k·T`）的实际执行量，用 `Step(n)` 推进
`n` 个 tick；`Mode()`、`Stats()`、`RunAt(t)`、`Now()` 为查询接口。
所有操作与查询由同一把互斥锁保护，并发调用等价于某个串行顺序。

### 每个时刻的三步次序

`Step(n)` 依次处理 n 个 tick。对时刻 `t`（tick 从 0 起）严格按以下顺序：

1. **错过截止期**：对每个未完成且 `d == t` 的作业记一次错过（按任务关键级
   分别计入 `MissedLO` / `MissedHI`）并移除。HI 任务在任一模式下错过都计数。
2. **恢复判定**：若当前为 HI 模式且此刻没有任何未完成作业，恢复为 LO
   （`Recoveries++`）。
3. **释放作业**：释放所有满足 `t >= φ` 且 `(t-φ) mod T == 0` 的作业；
   HI 模式下 LO 任务的释放被**跳过**（`Skipped++`，作业序号照占）；
   否则作业获得剩余量=实际执行量、已执行量 0、`d = t+T`。

三步之后，在未完成作业中取 `prio` 最小者运行一个 tick（已执行量 +1、
剩余量 -1）。

### 切换与恢复判据

- **切换（LO → HI）**：在 tick 末判定，仅当模式为 LO、运行的是 HI 作业、
  剩余量未归零且已执行量恰等于其 `CL` 时切换（`Switches++`）。即实际执行量
  **恰等于** `CL` 时作业完成则不切换；只有超出 `CL`（`c > CL`）才会切换，
  切换发生在已执行量达到 `CL` 的那个 tick 的末尾，而非下一 tick 开始。
- **恢复（HI → LO）**：只发生在上面第二步——HI 模式且无任何未完成作业时。
  因此“恢复先于释放”：某时刻恰好空闲时，先恢复 LO，再按 LO 模式释放
  （此刻释放的 LO 作业不会被跳过）。错过判定先于恢复判定：`d=t` 的作业先
  被移除，若错过使系统恰好变空闲，则同一 tick 即恢复。
- HI 模式内运行的 HI 作业不再触发切换。

### 舍弃与跳过的区别

- **舍弃（Discarded）**：LO → HI 切换瞬间，所有*已释放且未完成*的 LO 作业
  被立即移除，按个数计入 `Discarded`。被舍弃的作业不计完成也不计错过。
- **跳过（Skipped）**：HI 模式期间，LO 任务*到点释放*时不创建作业，计入
  `Skipped`；作业序号 `k` 仍然占用（下一次释放使用更大的 `k`），对被跳过
  作业预设的 `SetDemand` 不产生任何效果。被跳过同样不计完成/错过。

### 拒绝原因（只报按序第一个）

- `AddTask`：`ErrStarted`（已执行过 tick）→ `ErrInvalidParam` →
  `ErrDuplicateID` → `ErrDuplicatePrio` → `ErrTaskTableFull`。
- `SetDemand`：`ErrUnknownTask` → `ErrInvalidParam`（`k<0` 或 `c` 越界）→
  `ErrJobReleased`（释放时刻早于当前时刻）。对同一作业重复设置以最后一次为准。
- `Step`：`n` 不在 `[1, 10^6]` 返回 `ErrInvalidParam`。
- 任何被拒绝的操作都不改变任务、需求、模式、计数与当前时刻。

### 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test ./mcsched/ -v                         # 定向用例（含三个题述示例）
go test ./mcsched/ -run TestNaiveDifferential2000 -v   # 与朴素逐步模拟对拍 2000 组
go test -race ./...                           # 竞态检测
go vet ./... && gofmt -l .
```

对拍用例（`mcsched/naive_test.go`）内置一份独立的朴素参考实现
（map 管理作业、全量扫描，不与生产实现共享代码），并在日志中打印每组的
输入（任务/需求/时长）、输出（轨迹/计数/模式）与“判定：一致”依据；
同时用随机切分的多次 `Step` 调用验证 `Step(a+b)` 与 `Step(a);Step(b)`
逐位等价。
