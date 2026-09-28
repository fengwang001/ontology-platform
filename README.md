# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 跳跃窗口计数器（`hopping` 包）

并发安全的跳跃（hopping / sliding-with-hop）窗口计数器：一个事件可同时
计入多个相互重叠的窗口，窗口由调用方驱动的时钟（水位）统一关闭并输出计数。

### 窗口划分与归属

- 配置：`WindowSize`（窗长 W）、`SlideStep`（步长 S，0 < S ≤ W）。
  第 k 个窗口为 **左闭右开** 区间 `[k*S, k*S+W)`，窗口终点为 `k*S+W`。
- 时间戳 `t` 的事件属于**所有**满足 `start ≤ t < start+W` 的窗口；
  S < W 时窗口重叠，事件同时计入多个窗口；S = W 时退化为不重叠的翻滚窗口。
- 事件计数 `count` 必须为正，会累加到每个归属窗口内对应键的计数上。
- 归属基于整数下标推导（`floor(t/S)`、`ceil((t-W+1)/S)`），对**负时间戳**
  与事件**恰好落在窗口起点/终点**同样成立：起点包含、终点排除
  （如 t=5 属于 `[5,15)` 而不属于 `[-5,5)`）。

### 时钟推进、关闭与丢弃

- `Advance(watermark)` 推进时钟，时钟**只进不退**：
  传入小于当前水位的值返回 `ErrClockRewind` 且不改变任何状态；
  推进到当前水位是空操作。初始水位为 `math.MinInt64`。
- 推进时关闭所有**终点 ≤ 新水位**的窗口，返回每个窗口每个键一条
  `WindowCount`，按 **(窗口终点, 键)** 升序排列；**每个窗口只输出一次**，
  关闭后即从内存删除。
- `Add` 时事件只计入**尚未关闭**的归属窗口：
  - 部分窗口已关闭、部分仍打开 → **部分迟到**，只计入仍打开的窗口；
  - 所有归属窗口均已关闭 → **整条丢弃**，`DroppedEvents + 1`，不报错；
  - 若接收该事件需要新建窗口、使同时保留的打开窗口数超过
    `MaxOpenWindows`，返回 `ErrTooManyOpenWindows`。

### 拒绝原因（可用 `errors.Is` 区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | 窗长 ≤ 0、步长 ≤ 0、步长 > 窗长、容量 ≤ 0 |
| `ErrEmptyKey` | 事件键为空字符串 |
| `ErrInvalidCount` | 计数增量 ≤ 0 |
| `ErrInvalidTimestamp` | 时间戳会使归属窗口端点溢出 int64 |
| `ErrClockRewind` | 时钟回退（新水位 < 当前水位） |
| `ErrTooManyOpenWindows` | 同时保留的打开窗口数将超过上限 |

任何被拒绝的操作都不会改变时钟、计数、丢弃数或已输出结果
（拒绝前只做纯计算，不写入状态）。

### 并发与确定性

- 单个 `sync.RWMutex` 保护全部状态；`Snapshot()` 在读锁下返回逐字段一致的
  `Stats`（水位、打开窗口数、缓存计数、丢弃数、已输出窗口/记录数），
  可与写入并发调用。
- 输出切片在锁内组装完成后返回，调用方可自由读写。
- 输出顺序只取决于输入序列，与并发调度无关；同一序列反复计算得到
  完全相同的结果。并发测试将结果与独立的朴素参照（直接枚举窗口下标、
  用 `start ≤ t < start+W` 判定）逐字段比对。
- 设置 `Config.Logger`（签名同 `log.Printf`）后，日志会打印每次
  ADD/ADVANCE 的输入、ACCEPTED/DROPPED/REJECTED 判定依据及输出明细。
  注意 Logger 实现不得回调同一个计数器（会在锁上自死锁）。

最小用例见 [`hopping/example_test.go`](hopping/example_test.go)。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 多轮重复（并发用例）
go test -race -count=5 ./hopping

# 查看详细输出（含 Example 与各用例）
go test -v ./hopping

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

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
