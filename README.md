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

## 逗留时间主动丢包队列（`dwellq` 包）

`dwellq` 是一个按队首包**逗留时间**（sojourn time，出队时刻 − 入队时刻）进行主动队列管理的有界 FIFO，语义对标 CoDel：持续拥塞时以递增频率丢包，把排队时延压回目标附近；短暂突发（不足一个观察期）不被误伤；决策过程无随机数，相同时钟与操作序列可逐包复现。

### 参数与术语

- `T`（target，目标逗留时间）、`I`（interval，观察期）、`maxPacketSize`（最大包长）、`byteCapacity`（字节容量）。
- 所有时刻均由调用方以 `int64` 传入，单位自定（如纳秒），`T`、`I`、`now` 必须同单位。
- 入队时若 `usedBytes + size > byteCapacity`，到达包被**尾丢**（`Enqueue` 返回 `tailDropped=true`，不是错误），队列不变。
- 出队队首包 `p` 时，`dwell = now − enqAt(p)`；记放行 `p` 后的剩余字节为 `remaining`。
- **未超标**：`dwell < T` **或** `remaining <= maxPacketSize`（二者任一成立）。反之 `dwell >= T` 且 `remaining > maxPacketSize` 为**超标**（逗留恰等于 `T` 算超标）。

### 状态迁移

管理器只有两个状态：**非丢弃状态**与**丢弃状态**；非丢弃状态额外维护「首次超标时刻」`firstAbove`（实际存的是 `首次超标时刻 + I`，0 表示未设置）。

**非丢弃状态**（每次出队）：

1. 队空 → 返回空，`firstAbove` 清零。
2. 队首包未超标 → 放行，并清除 `firstAbove`（这保证短暂突发不会累积计时）。
3. 队首包超标且 `firstAbove` 未设置 → 设置 `firstAbove = now + I`，放行该包。
4. 队首包超标且 `now < firstAbove` → 放行（仍在一个观察期的宽限内）。
5. 队首包超标且 `now >= firstAbove` → **丢弃该包并进入丢弃状态**，然后继续判定下一个包。

**丢弃状态**（循环判定后续包）：

- 队首包未超标 → **退出丢弃状态**并放行该包。
- 超标且 `now >= nextDrop` → 丢弃它，计数加一，推进 `nextDrop`，继续判定下一个包。
- 超标但 `now < nextDrop` → 放行该包（仍留在丢弃状态）。
- 连续丢到队空 → 返回空并退出丢弃状态。
- 任意时刻队空出队都返回空、退出丢弃状态并清除 `firstAbove`。

### 计数与下次丢弃时刻

- **进入丢弃状态时**：若距上次退出不足 `16·I`（`now − lastExitTime < 16·I`）且上次退出时计数 `lastExitCount > 2`，则 `count = lastExitCount − 2`（快速重入沿用旧烈度）；否则 `count = 1`。首次进入时 `nextDrop = now + I/√count`。
- **丢弃状态下每丢一个包**：先 `count += 1`，再 `nextDrop = nextDrop + I/√count`，间隔随计数按平方根倒数收缩：`I, I/√2, I/√3, …`。
- `I/√count` 取满足 `d²·count <= I²` 的**最大整数** `d`（用 `math/big` 精确整数开方，避免浮点误差与乘法溢出）。
- 退出丢弃状态（未超标放行或队空）时保存 `lastExitTime = now`、`lastExitCount = count`。

### 拒绝原因（可区分，被拒操作不改任何状态）

| 哨兵错误 | 触发条件 |
| --- | --- |
| `dwellq.ErrInvalidParam` | `T`、`I`、`maxPacketSize`、`byteCapacity` 任一非正 |
| `dwellq.ErrTargetTooLarge` | `T >= I` |
| `dwellq.ErrInvalidPacket` | 包长非正或超过 `maxPacketSize` |
| `dwellq.ErrClockRewind` | `Enqueue`/`Dequeue` 的 `now` 早于此前观察到的最大时刻 |

被拒绝的操作不会改变队列、计数或任何时刻字段。

### 并发与不变量

- `Enqueue`、`Dequeue` 可被多 goroutine 并发调用，内部由互斥锁保护；算法本身无随机数、无睡眠。
- 恒有：`入队成功数 == 正常出队数 + 主动丢包数 + 队内包数`（尾丢不计入入队成功数）。可通过 `Stats()` 读取各项计数。
- 确定性：相同的入队、出队与时钟序列重放，得到相同的丢包序列。
- 每次操作的返回值都带 `Reason` 字符串，记录输入、输出与判定依据（例如 `set firstAbove=...`、`now < nextDrop=...`、`EXIT dropping state`）。

### 本地验证

```bash
# 全量测试（带竞态检测；需要 /usr/local/go/bin 在 PATH 中）
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go test -race -v ./dwellq

# 仅跑指定场景
go test -run 'TestShortBurst|TestFirstDrop|TestDropIntervals|TestReentry|TestEmptyQueue|TestTailDrop' -v ./dwellq

# 覆盖率与静态检查
go test -coverprofile=coverage.out ./dwellq
go tool cover -html=coverage.out
gofmt -l .
go vet ./...
```

测试覆盖：超标不足一个观察期的突发不丢、首次丢包恰在 `I` 之后、丢包间隔按平方根收缩、逗留恰等于 `T`、`16·I` 内重入沿用计数（及超过 `16·I` 重置）、队空退出、尾丢、参数/包长/时钟回拨拒绝、并发不变量与重放确定性；`-v` 日志打印每次操作的输入、输出与判定依据。
