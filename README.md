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

## 跳跃窗口计数器（`hoppingwindow` 包）

`hoppingwindow.Counter` 把事件计入多个重叠的跳跃（滑动）窗口，窗口的关闭由调用方
通过只进不退的时钟显式推进。

### 参数

| 参数 | 含义 | 约束 |
| --- | --- | --- |
| `WindowSize` | 窗口长度 | `> 0` |
| `Slide` | 相邻窗口起点的步长 | `0 < Slide <= WindowSize` |
| `MaxOpenWindows` | 同时保留的、尚未关闭窗口数上限 | `> 0`，且不小于单事件最大重叠数 `ceil(WindowSize/Slide)` |
| `Logger` | 可选日志（`Printf` 接口） | 为 `nil` 时不打印输入、输出与判定依据 |

### 窗口归属规则

- 窗口相对 Unix 纪元对齐：第 `k` 个窗口为左闭右开区间 `[k*Slide, k*Slide+WindowSize)`，
  对负时间戳同样成立（向下取整除法，不使用截断除法）。
- 事件时间戳 `t` 属于**所有**满足 `start <= t < start+WindowSize` 的窗口，即
  `t-WindowSize < start <= t` 范围内的全部步长起点。窗口长度不是步长整数倍时
  （如 size=10s、slide=3s），一个事件也会同时计入多个窗口。
- 恰好落在窗口起点的事件属于该窗口；恰好落在窗口终点的事件不属于该窗口（左闭右开）。

### 关闭与输出规则（`Advance(t)`）

- 时钟只进不退：`t` 早于当前时钟返回 `ErrClockRegression`，且不改变任何状态；
  `t` 等于当前时钟是空操作。时钟从未推进时语义等价于 `-∞`，不关闭任何窗口。
- 推进到 `t` 时关闭所有终点 `start+WindowSize <= t` 的窗口。
- 输出按**窗口终点（即起点）升序、同一窗口内按键字典序**排列；每个窗口只输出一次，
  重复推进不会再次得到已关闭窗口。
- 无论一次跳到很远还是分多次小步推进，同一输入序列的累计输出完全相同。

### 迟到与丢弃规则（`Record(t, key)`）

- 事件只计入**尚未关闭**的包含窗口；终点已过的窗口被跳过。
- 部分迟到：部分包含窗口已关闭、部分仍打开时，事件仍计入仍打开的窗口，不丢弃。
- 整条丢弃：所有包含窗口均已关闭时，事件被丢弃，`Snapshot().Dropped` 加一。
- 空键返回 `ErrEmptyKey`；事件会使打开窗口数超过 `MaxOpenWindows` 时返回
  `ErrTooManyOpenWindows`（已打开的窗口不重复计数）。关闭部分窗口释放名额后即可恢复写入。
- 所有被拒绝的操作（非法参数、空键、时钟回退、超窗数）都不会改变时钟、计数、
  丢弃数或已输出结果。

### 错误判定

所有错误均为 `*hoppingwindow.Error`，按 `Kind` 区分，可用 `errors.Is` 判断：

| 错误 | `Kind` | 触发条件 |
| --- | --- | --- |
| `ErrInvalidConfig` | `KindInvalidConfig` | 构造参数非法 |
| `ErrEmptyKey` | `KindEmptyKey` | 事件键为空 |
| `ErrClockRegression` | `KindClockRegression` | `Advance` 试图把时钟往回拨 |
| `ErrTooManyOpenWindows` | `KindTooManyOpenWindows` | 同时保留的打开窗口数超限 |

### 并发与一致性

所有方法在互斥锁下完成，`Snapshot()` 返回的时钟、丢弃数与各窗口计数逐字段一致
（不会读到半更新状态）；并发写入结果与朴素参照实现一致（见
`TestConcurrentWritesMatchNaiveReference`），同一输入序列反复计算得到完全相同的输出。

### 本地验证

```bash
# 全量测试（含负时间戳、边界、部分迟到、丢弃、关闭顺序、非法输入、并发）
go test -race -v ./hoppingwindow

# 反复运行以确认确定性
go test -race -count=10 ./hoppingwindow

# 查看日志样例（打印输入、输出与计入/丢弃/拒绝的判定依据）
go test -run TestLoggerPrintsInputsOutputsAndDecisions -v ./hoppingwindow

# 覆盖率与静态检查
go test -coverprofile=coverage.out ./hoppingwindow
go tool cover -html=coverage.out
go vet ./hoppingwindow
```

