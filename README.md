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

## 16 倍过采样 UART 接收器

UART 接收器位于 `uart` 包，构造方式为：

```go
receiver, err := uart.NewReceiver(uart.ParityEven, 8)
err = receiver.Feed(level)
err = receiver.FeedAll(levels)
frame, err = receiver.Pop()
```

校验模式为 `uart.ParityNone`、`uart.ParityEven`、`uart.ParityOdd`；队列深度必须不小于 1。`Feed` 与 `FeedAll` 只接受电平 `0` 或 `1`，非法参数会分别返回 `ErrInvalidParity`、`ErrInvalidQueueDepth`、`ErrInvalidLevel` 或 `ErrQueueEmpty`，且被拒绝的操作不改变状态与采样点序号。`FeedAll` 会先校验整批输入，任一电平非法则整批拒绝。

### 采样序号与起始位对齐

每次成功调用 `Feed` 消费一个采样点，采样点序号从 0 开始递增，并始终等于成功送入的采样点总数。

空闲态遇到低电平时记录起始候选序号 `s`。序号 `s+1` 到 `s+6` 不处理；在 `s+7`（起始位第 8 个点，即中点附近）：

- 线上为高：判定为毛刺，`GlitchCount()` 加 1，回到空闲态，该点不再作为新的起始候选。
- 线上为低：起始位成立，继续接收。

设 `q=1` 表示有校验位，`q=0` 表示无校验位。之后的采样序号为：

- 数据位 `i=0..7`：`s+7+16*(i+1)`，低位在前。
- 校验位（如有）：`s+7+16*9`。
- 停止位：`s+7+16*(9+q)`，无校验时为 `s+151`，有校验时为 `s+167`。

停止位为高的帧结束后立即回到空闲态，因此下一个采样点若为低即可成为下一帧的起始候选。停止位为低的帧结束后进入等待态，必须先看到一个高电平才回到空闲态；该恢复高电平不会被当作起始候选。

### 帧结果与错误判定

`Pop` 返回的 `Frame` 包含：

- `Byte`：8 个数据位按低位在前组装出的字节。
- `ParityError`：统计 8 个数据位与校验位中 `1` 的个数；偶校验要求偶数，奇校验要求奇数。无校验时恒为 `false`。
- `FramingError`：停止位采样值为低时置位。
- `Break`：停止位为低，且所有数据位和校验位（如有）均为低时置位。
- `EndSampleIndex`：停止位采样点的全局序号。

中止帧一定同时是帧错误；奇校验下全 0 数据位加 0 校验位的中止帧还会同时带校验错误。

### 接收队列、溢出与并发

帧在停止位采样点产出。若队列未满则进入环形 FIFO；若已满则丢弃新帧，`OverflowCount()` 加 1，队列中已有帧保持不变。`ProducedCount()` 等于成功入队帧数加溢出帧数，因此：

```text
产出帧总数 = 入队帧数 + 溢出帧数
```

所有送入、取出和查询方法都通过同一个互斥区串行化，外部并发调用的结果等价于某个串行执行顺序。`FeedAll` 持锁处理完整批次，因此对同一电平序列的任意切分，重放得到的帧、毛刺数、溢出数和采样点序号完全相同。

可用的查询方法包括 `Len()`、`SampleCount()`、`GlitchCount()`、`OverflowCount()`、`ProducedCount()`、`EnqueuedCount()` 和 `Snapshot()`。

### UART 本地验证

```bash
go test -race -v ./uart
go test ./...
go vet ./...
```

`uart/receiver_test.go` 覆盖起始毛刺与成立、所有数据位/校验位/停止位的精确序号、偶/奇校验全 0 与全 1、非中止帧错误、奇校验中止及等待高电平恢复、停止位后立即启动下一帧、队列满保留旧帧并计溢出、非法操作原子拒绝、所有切分点一致性，并包含一个按规则独立编写的逐点朴素波形解码器作为对照。`go test -v` 日志会打印输入、输出帧和判定依据。
