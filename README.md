# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 窗口看门狗（`watchdog` 包）

带注入时钟的窗口看门狗模型，代码见 `watchdog/watchdog.go`。

### 窗口与时间的取法

- `New(t0, open, close, pre)` 要求整数参数满足 `0 <= open < close`、`0 <= pre < close`，否则 panic（`ErrInvalidConfig`）。
- 上次喂狗时刻记为 `f`，初值为 `t0`；经过时间 `d = t - f`。时间为离散整数时刻。
- 合法喂狗区间为 **左闭右开** 的 `open <= d < close`：
  - `d < open`：喂狗过早，立即在时刻 `t` 复位，原因为 `Early`。
  - `open <= d < close`：有效喂狗，`f` 更新为 `t`，并以新周期重新武装预警。
  - `d` 达到 `close`：无论是否有人喂狗，都在时刻 `f+close` 复位，原因为 `Timeout`。
- `pre > 0` 时在时刻 `f+close-pre` 产生一条 `Warning` 预警；`pre == 0` 不预警。
- 复位后看门狗**锁存在复位态**，不再产生任何事件；只有 `Restart` 能以新时刻重新开始。

### 事件顺序：到点事件先于操作

每次 `FeedAt(t)` / `TickAt(t)` / `RestartAt(t)` 都先「补记到点事件」：把时刻不大于 `t` 且尚未记录的预警与超时复位按时刻先后追加到事件表，然后才处理本次操作。

- 同一周期内预警时刻严格早于超时复位（`f+close-pre < f+close`），故同一次补记中预警在前。
- `d == close-pre` 时喂狗：先记预警，再按窗口内喂狗处理。
- `d == close` 时喂狗：先记 `Timeout` 复位，喂狗随后落在复位态被拒（`ErrFeedWhileReset`）。
- 预警点可能早于 `open`；此时过早喂狗会先补记预警，再追加同刻的 `Early` 复位（两条事件时刻相等、预警在先）。
- `TickAt` 只补记；`RestartAt` 在补记之后才判定，所以超时恰在 `t` 到点时 `Restart(t)` 仍然有效。

### 拒绝原因与对状态的影响

| 情形 | 错误 | 状态影响 |
| --- | --- | --- |
| 操作时刻小于上一次操作时刻（首个操作与 `t0` 比较） | `ErrTimeRewound` | **不改变任何状态**，已补记事件也不保留 |
| 复位态下 `Feed` | `ErrFeedWhileReset` | 保留已补记的到点事件，上一次操作时刻推进到 `t` |
| 喂狗过早（`d < open`） | `ErrFeedTooEarly` | 保留补记事件并追加 `Early` 复位；唯一会改变看门狗状态的拒绝 |
| 非复位态下 `Restart` | `ErrRestartNotReset` | 保留已补记事件，上一次操作时刻推进到 `t` |
| 构造参数不合法 | panic 包装 `ErrInvalidConfig` | 对象不可用 |

### 并发与确定性

所有操作与查询（`Events`、`InReset`）由互斥锁保护，结果等价于某一串行顺序；事件表时刻非递减，每个周期至多一条预警与一条复位，复位后到下次 `Restart` 前无新事件。相同操作序列重放得到完全相同的事件表。

时间可显式传入（`FeedAt` / `TickAt` / `RestartAt`），也可通过注入的 `Clock`（`Feed` / `Tick` / `Restart`）驱动，便于测试。

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

# 看门狗包：带竞态检测与详细判定日志（与逐时刻朴素模拟对照）
go test -race -v ./watchdog
go test -run TestExactWindowBoundaries -v ./watchdog

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
