# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 累积窗口计数器（`window` 包）

`window.Accumulator` 是一个带水位线（watermark）的**累积窗口计数器**：在每个
大窗口内部按步长逐级扩大子窗口，每到一个子窗口终点，输出从大窗口起点到该
终点的累计计数。

### 大窗口

- 按固定长度 `WindowSize` 对齐划分，**左闭右开**：第 k 个大窗口为
  `[k*WindowSize, (k+1)*WindowSize)`。
- 每个事件按时间戳归入**唯一**大窗口；负时间戳向下取整对齐（例如
  WindowSize=10 时，t=-6 归入 `[-10, 0)`，t=0 归入 `[0, 10)`）。

### 子窗口（逐级扩大）

- 大窗口内以 `Step` 为步长定义终点：`start+Step, start+2*Step, …,
  start+WindowSize`（`Step` 必须整除 `WindowSize`）。
- 子窗口共享同一个起点（大窗口起点），终点逐级外扩，因此计数天然累积：
  第 j 个子窗口覆盖 `[start, start+j*Step)`。
- 事件计入**所有终点严格晚于其时间戳**的子窗口。例如 t=5 不计入终点 5
  （终点 5 的区间是 `[start,5)`），只计入终点 10、15……

### 触发与累积输出

- 子窗口在其**终点 ≤ 水位线**时触发一次，输出
  `{Key, WindowStart, End, Cumulative}`，`Cumulative` 为区间
  `[WindowStart, End)` 内的事件总数。
- **计数为零也照常输出**；每个子窗口恰好触发一次，顺序按 `(End, Key)`
  确定，保证同一输入序列的输出完全一致、可复现。
- 一个大窗口的全部子窗口触发完毕后，其内部状态被回收。

### 水位线与丢弃

- 水位线随已接收事件的**最大事件时间**单调前进，初始值为负无穷
  （`math.MinInt64`），因此任何首个事件（含负时间戳）都不会被误丢。
- 迟到判定：事件的最小子窗口终点 `MinEnd`（严格晚于事件时间的第一个
  终点）**≤ 当前水位线**时丢弃，丢弃计数 `Dropped` 加一。
  - 例：WindowSize=10、Step=5，水位线已到 5，则迟到的 t=0（MinEnd=5）
    因 `5 <= 5` 被丢弃——**恰好相等也丢弃**。
- 丢弃不是错误：`Add` 返回 `AddResult{Dropped: true}`，调用方与拒绝
  （返回 error）区分。

### 拒绝规则（三类可区分原因）

| 场景 | 错误 |
| --- | --- |
| `WindowSize<=0`、`Step<=0`、`Step` 不整除 `WindowSize`、`MaxWindows<=0` | `ErrInvalidParameter` |
| 事件键为空字符串 | `ErrEmptyKey` |
| 同时保留的（键 × 大窗口）状态数会超过 `MaxWindows` | `ErrTooManyWindows` |

被拒绝的输入**不改变任何状态**：水位线、丢弃数、已输出结果均保持不变
（容量裁决在提交任何变更之前完成；到期回收腾出的名额会计入裁决）。

### 并发与一致性

- `Add` 与 `Snapshot` 均在互斥/读写锁保护下访问内部状态，可并发调用。
- `Snapshot()` 返回水位线、丢弃数与全部已输出结果的**逐字段一致**快照，
  输出切片为防御性拷贝，调用方修改不影响计数器内部状态。

### 日志

每次 `Add` 都通过 `slog` 记录输入（键、时间戳）、输出（本次触发的累计
结果）与判定依据（`accepted` / `dropped: minEnd <= watermark` /
`rejected: empty-key|too-many-windows`，含窗口起点、MinEnd、水位线前后
值等）。可用 `window.WithLogger(logger)` 自定义输出位置。

### 快速示例

```go
acc, _ := window.New(window.Config{WindowSize: 10, Step: 5, MaxWindows: 100})
res, err := acc.Add("sensor-a", 7)
snap := acc.Snapshot() // 并发安全：Watermark / Dropped / Outputs
```

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

## 测试与本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（window 包用例覆盖负时间戳归属、边界事件、
# 水位线恰好等于最小子窗口终点时丢弃、零计数输出、各类非法输入、
# 容量超限拒绝无副作用、确定性与并发快照一致性）
go test -race -v ./...

# 单个包 / 单个用例
go test ./window
go test -run TestDropWhenWatermarkEqualsMinEnd ./window

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
