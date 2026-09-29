# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多实例周期任务触发协调器（`scheduler` 包）

位于 `scheduler/scheduler.go`，解决多个实例在任期（term）转移、本地时钟跳变、
实例执行中途失效等情况下，对同一组周期触发时刻的"全局至多执行一次 + 错过补偿"协调问题。

### 序号计算

- 第 k 个触发时刻为 `t(k) = Anchor + k*Period`（k 从 0 起）。
- 实例在本地时刻 `now` 被唤醒时：
  - `now < Anchor`：m 不存在，什么也不做。
  - 否则 `m = floor((now - Anchor) / Period)`，即不晚于 now 的最大序号。
- 时钟回拨自然落入"m 不存在或 m <= r"分支，不会重复执行；
  时钟前跳 N 个周期则 m 一次性前进 N，交由所选策略决定补偿集合。

### 共享记录与推进/执行的先后（关键不变量）

所有实例共享一份执行记录：最后已执行序号 `r`（初始 -1）与写入它的任期 `rTerm`。
对每个准备执行的序号 seq，严格按以下顺序操作：

1. 加锁，检查 `term >= rTerm` 且 `seq > r`（任期是否仍有效、序号是否未处理）；
2. **先在锁内把记录原子推进到 `(seq, term)`，再释放锁**；
3. 锁外执行真正的任务体 `Executor(seq)`。

这样安排的理由：

- 记录推进是全局唯一的"认领点"，天然互斥 ⇒ 每个序号至多执行一次；
- 任务体在锁外运行，不阻塞其他实例的唤醒与认领；
- 若实例在执行中途崩溃，seq 的认领已经落盘式写入共享记录，
  任何实例（包括它自己重启后）重试都会因 `seq <= r` 跳过它 ⇒ 失效时刻绝不重做；
- 按记录推进的先后，执行序号严格递增（任务体按认领顺序启动，且认领单调）；
- 记录一旦被更高任期写入（`term < rTerm`），旧任期实例立即停止，不再执行。

### 唤醒判定流程

1. 未持有任何已授任期的实例唤醒 → 返回 `unauthorized_instance` 拒绝，记录不变。
2. 求 m；m 不存在或 `m <= r` → 什么也不做。
3. `term < rTerm`（记录被更高任期写过）→ 放弃，不报错、不执行。
4. 否则按策略生成待执行/跳过集合，逐序号"认领 → 执行"。
5. 策略判定跳过的序号（补偿窗口外、容忍窗外）不执行，但记录仍推进到 m。

### 三种补偿策略

| 策略 | 行为 |
| --- | --- |
| `CatchUpAll`（全部补偿，需 `K>0`） | 按升序执行 `r+1..m` 中最近的至多 K 个：窗口外序号记入跳过、记录一并推进；升序执行保证业务时序。 |
| `CatchUpOne`（补一次） | 只执行 m；`r+1..m-1` 跳过，记录推进到 m。 |
| `TolerantSkip`（容忍跳过，需 `Tolerance>=0`） | 仅当 `now - t(m) < Tolerance` 时执行 m；否则不执行，但记录推进到 m。边界为严格小于：延迟恰好等于容忍时长时判定跳过。 |

### 拒绝原因（`RejectError.Reason`，互不相同）

- `invalid_period`：周期非正；
- `invalid_k`：全部补偿策略下 K 非正；
- `invalid_tolerance`：容忍时长为负；
- `stale_term`：授予任期不大于已授出的最大任期；
- `unauthorized_instance`：唤醒一个从未获授任期的实例。

被拒绝的操作不修改任何共享状态。

### 日志

协调器通过 `slog` 打印每次授予/唤醒的**输入、输出与判定依据**
（m、r、记录任期、所选策略的判定式、逐序号认领/执行/中途失效）。
默认输出到 stderr，可通过 `Config.LogOutput`（如 `io.Discard`、`testing.T` 日志）重定向。

### 本地验证

```bash
go test -race -v ./scheduler
go test -race -count=10 ./scheduler   # 反复压测并发交错
go vet ./...
gofmt -l .
```

测试覆盖：转移后旧任期实例唤醒（让/不让两种时序）、时钟回拨、
前跳 100 个周期下三种策略的执行集合、触发时刻与容忍边界（含 1ns 窗内与恰在边界）、
执行中途失效不重做、多实例并发唤醒的全局唯一性与严格递增、相同序列重放确定性。

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
