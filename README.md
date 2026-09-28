# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 带水位线的累积窗口计数器（`counter` 包）

`counter` 包实现了一个按 **键（key）** 分组、带 **水位线（watermark）** 的
**累积窗口计数器**。代码位于 [`counter/`](counter/)。

### 大窗口（固定长度、左闭右开）

- 时间轴按固定长度 `WindowSize` 划分为左闭右开的大窗口
  `[start, start+WindowSize)`，起点满足 `start` 是 `WindowSize` 的整数倍。
- 划分对**负时间戳同样成立**，采用向下取整（floor）。例如
  `WindowSize=10` 时：`ts=9 → [0,10)`、`ts=10 → [10,20)`、
  `ts=-1 → [-10,0)`、`ts=-10 → [-10,0)`。
- 每个事件按时间戳归入**唯一**大窗口；恰好落在边界的事件属于右侧窗口
  （左闭右开）。

### 子窗口与累积计数

- 每个大窗口内部按固定步长 `Step`（要求整除 `WindowSize`）切出若干
  子窗口终点：`start+Step, start+2*Step, …, start+WindowSize`。
- 事件时间戳 `e` 被计入该大窗口内**所有终点晚于 `e`** 的子窗口
  （即终点 `t` 满足 `t > e`）。事件对应的“最小子窗口终点”为
  `start + floor((e-start)/Step)*Step + Step`；当 `e` 恰好等于某个终点时，
  该终点区间左闭右开不含 `e`，最小终点是下一个终点。
- 某子窗口终点到期触发时，输出从**大窗口起点到当前终点**的累计计数
  （对各步长桶做前缀和）。同一大窗口内累计计数单调不减。

### 水位线、触发与丢弃

- 水位线随**被接收**事件的最大时间戳单调前进（只增不减）。
- 每当子窗口终点 `end <= 水位线` 时该子窗口到期，**各触发一次**并输出
  `Result{Key, WindowStart, End, Count}`；**计数为零也输出**；水位线一次
  跳跃跨过多个终点时，这些终点会逐个连续输出。
- 事件在其**最小子窗口终点不超过（`<=`）水位线**时判定为迟到并**丢弃**
  （计入 `Dropped`）。注意是“终点 ≤ 水位线”即丢弃——例如水位线恰为 `5`
  时，最小终点为 `5` 的事件被丢弃。被丢弃事件不计入任何计数、不推进水位线。
- 尚无任何被接收事件时水位线为“未设置”（快照中 `WatermarkSet=false`、
  `Watermark=math.MinInt64`），因此首个事件即使是负时间戳也不会被误丢弃。

### 拒绝（有可区分原因，且无副作用）

以下输入会被拒绝，返回 `*counter.RejectError`（可用 `errors.Is` 匹配
`ErrInvalidConfig` / `ErrEmptyKey` / `ErrTooManyWindows`，或读取 `Code`），
且**不改变水位线、丢弃数或已输出结果**：

| 原因码 | 触发条件 |
| --- | --- |
| `invalid_config` | `WindowSize<=0`、`Step<=0`、`Step` 不整除 `WindowSize`、`MaxWindows<=0` |
| `empty_key` | 事件的键为空字符串 |
| `too_many_windows` | 事件需新建大窗口，而该键当前保留的大窗口数已达 `MaxWindows` |

大窗口一经创建即计入“保留窗口数”（即使其子窗口已全部触发，仍保留空壳、
释放计数桶内存），以此约束每个键的键控状态规模；迟到事件会被丢弃规则拦住，
不会重建或改动已完成窗口。

### 并发、确定性与日志

- `Counter` 可被多 goroutine 并发使用：变更走写锁、`Snapshot()` /
  `Watermark()` / `Dropped()` / `Outputs()` 走读锁；`Snapshot()` 返回
  **深拷贝**，字段逐字段一致、与后续写入隔离。
- 输出按“触发批次 +（键、大窗口起点、终点）”的确定顺序追加，
  **同一输入序列反复计算得到完全相同的输出**。
- 每条输入都记录判定依据（`ACCEPT` / `DROP` / `REJECT` 及水位线、
  最小子窗口终点等比较），每次触发记录 `EMIT`。可用
  `counter.WithLogger(io.Writer)` 重定向日志。

最小用法见 [`counter/example_test.go`](counter/example_test.go)。

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

## 测试与本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（计数器并发用例建议始终带 -race）
go test -race -v ./...

# 单个包 / 单个用例
go test ./counter
go test -run TestNegativeTimestamps -v ./counter
go test -run ExampleCounter -v ./counter

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

计数器测试覆盖：负时间戳归属、恰好落在大窗口/子窗口边界的事件、
水位线恰好等于最小子窗口终点时的丢弃、零计数输出、水位线大跳变、
各类非法输入与拒绝无副作用、重复运行的确定性、快照隔离与并发竞态。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
