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

## 保留加回填调度器（`scheduler` 包）

多节点同构集群上的 conservative backfilling 调度器：为队首作业保留最早可行的
启动时刻，在不推迟该保留的前提下让后续作业先填入空闲节点。时间由注入时钟
（`scheduler.Clock`）以整数时刻推进，可通过 `New(n, start, logWriter)` 打开判定日志，
日志按 `input` / `reserve` / `backfill-*` / `event` / `reject` / `output` 标签打印
输入、输出与判定依据。

### 触发时机与调度流程

每次 `Submit`、`Finish`（提前结束）、`Advance`（时钟推进，含强制结束）或 `Query`
都在同一把互斥锁下执行下列流程，保证并发安全与可复现：

1. **强制结束**：所有 `StartTime + Duration <= now` 的运行作业按
   `(结束时刻, 作业标识)` 升序被强制结束并释放节点。
2. **立即启动队首**：按提交顺序循环，只要队首作业当前放得下（`Nodes <= 空闲节点`）
   就立即启动，直到出现放不下的队首为止。
3. **计算保留（影子时刻与富余数）**：若队首 `H` 放不下，把运行作业按预估结束时刻
   升序（并列按作业标识升序）排序，从当前空闲节点数开始逐个（同时刻成组）“释放”：
   - 第一个使累计可用节点 `available >= H.Nodes` 的释放时刻就是**影子时刻** `shadow`，
     即 `H` 最早可行的启动时刻；
   - **富余数** `surplus = available(在 shadow 时刻，含同时刻全部结束作业) - H.Nodes`。
4. **回填其余作业**：保持 `H` 在队首不动，按提交顺序检查后续作业 `B`，
   可回填当且仅当**现在放得下**（`B.Nodes <= 当前空闲`），并满足以下两类条件之一：
   - **短时回填（不跨影子）**：`now + B.Duration <= shadow`，`B` 在影子时刻前已结束，
     不占用富余数；
   - **富余回填（跨越影子）**：`B.Nodes <= surplus`，从 `surplus` 中扣除 `B.Nodes`。

   两类都不满足则跳过该作业（不终止检查，继续看后面的作业）。被回填的作业移出
   等待队列，`H` 仍保留在队首。

### 正确性性质

- **节点不超分**：任一时刻运行作业节点数之和不超过 `N`。
- **保留上界**：任一作业成为队首时记录的影子时刻，是它实际启动时刻的上界
  （`start <= shadow`）。短时作业在影子时刻前必已结束；富余作业只使用满足队首后
  仍多出的节点，因此不会把队首推迟到影子时刻之后。
- **确定可复现**：释放顺序按 `(end, id)` 打破并列，遍历一律按提交顺序；相同的
  提交、结束与时钟序列重放得到相同的启动序列与快照序列。

### 拒绝原因（互不相同，`errors.Is` 可判定）

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrInvalidNodes` | 集群节点数 `N <= 0` |
| `ErrInvalidJob` | 作业所需节点 `<= 0` 或 `> N` |
| `ErrInvalidDur` | 预估时长 `<= 0` |
| `ErrDuplicateID` | 作业标识在运行集合或等待队列中重复 |
| `ErrJobNotRunning` | `Finish` 的作业当前不在运行集合 |
| `ErrClockRewind` | 时钟目标时刻早于当前时刻（含回溯的 `Finish`） |

所有拒绝均在状态变更**之前**完成校验，被拒绝的操作不会改变队列、运行集合或时钟。

### API 速览

- `New(n int64, start int64, logW io.Writer) (*Scheduler, error)`
- `Submit(Job)`：非法作业被记录到日志并忽略（不改变状态）
- `Finish(id string, at int64) error`：`at` 不早于当前时刻；作业提前结束并重算保留
- `Advance(t int64) (Snapshot, error)`：推进时钟，到点作业强制结束并重新调度
- `Query() Snapshot`：返回 `Now`、确定性排序的 `Running` 与含 `Shadow`/`Surplus`
  的等待队列 `Queue`

### 本地验证

```bash
# 单元测试（覆盖：影子时刻边界回填、多作业分摊富余、提前结束重算、
# 强制结束、参数校验与无副作用、随机负载保留上界、重放确定性、并发安全）
go test -v ./scheduler/

# 竞态检测 + 重复执行
go test -race -count=3 ./scheduler/

# 全量测试 / 覆盖率 / 静态检查
go test ./...
go test -coverprofile=coverage.out ./scheduler/ && go tool cover -func=coverage.out
gofmt -l . && go vet ./...
```
