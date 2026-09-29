# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 逗留时间主动丢包队列

根包 `dwellqueue` 提供按出队逗留时间主动丢包的 `Manager`：

- `New(target, interval time.Duration, maxPacketSize, capacityBytes int)` 创建管理器。
- `Enqueue(packet []byte, now time.Duration)` 按字节容量入队；超容量返回 `tail_dropped`，不进入队列。
- `Dequeue(now time.Duration)` 返回一个出队包；同一次调用内主动丢弃的包记录在 `DequeueResult.Dropped`。
- `Stats()` 返回成功入队、正常出队、主动丢包、尾丢以及当前队列字节数。
- 时间由调用方显式传入单调时钟值，单位可由调用方选择；相同入队、出队和时钟序列重放会得到相同丢包序列。

### 判定与状态迁移

包的逗留时间为 `now - enqueuedAt`。同时满足以下两个条件才称为超标：

- 逗留时间不小于目标 `T`；
- 该包出队后剩余字节大于最大包长。

非丢弃状态：

- 当前包未超标时，清除首次超标时刻并正常返回。
- 第一次超标时不丢包，记录首次超标时刻为 `now + I`。
- 已记录首次超标时刻且 `now` 未早于该时刻时，主动丢弃当前包并进入丢弃状态，然后继续判定下一个包。

丢弃状态：

- 当前包未超标时立即退出丢弃状态，正常返回该包。
- 当前包超标且 `now < nextDropAt` 时正常返回该包，保持丢弃状态。
- 当前包超标且 `now >= nextDropAt` 时主动丢弃，递增计数，推进 `nextDropAt`，继续判定下一个包。
- 队空时出队返回空结果，退出丢弃状态并清除首次超标时刻。

### 计数与丢包频率

进入丢弃状态时：

- 若距离上次退出不足 `16I`，且上次退出时计数大于 2，则新计数为上次计数减 2；
- 否则新计数为 1。

丢包间隔使用整数：

```text
d(I, c) = floor(I / sqrt(c))
```

即最大的非负整数 `d`，满足 `d²·c ≤ I²`，避免浮点重放差异。首次进入时：

```text
nextDropAt = now + d(I, count)
```

丢弃状态中每丢弃一个包：

```text
count = count + 1
nextDropAt = nextDropAt + d(I, count)
```

因此持续拥塞时实际丢包间隔约按 `1/sqrt(count)` 收缩；退出后短暂恢复不会立即沿用高强度丢包。

### 拒绝原因

以下情况整体拒绝，不改变队列、计数、首次超标时刻或任何历史时刻：

- 参数非正：`ErrNonPositiveParameter`
- `T >= I`：`ErrTargetTooLarge`
- 包长非正：`ErrInvalidPacketLength`
- 包长超过最大包长：`ErrPacketTooLarge`
- 时钟回拨：`ErrClockRollback`

### 本地验证

```bash
# 全量测试，日志会打印每次输入、输出、状态值与判定依据
go test -v ./...

# 并发安全验证
go test -race -run TestConcurrentInvariant -count=50 ./...

# 格式化和静态检查
gofmt -w queue.go queue_test.go
go vet ./...
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
