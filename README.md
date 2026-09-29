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

## 限速回放器（`replay` 包）

`replay/replay.go` 提供 `Replayer[T]`：用**整数令牌桶 + 逻辑时钟**把入队事件按
固定速率平滑、保序地吐出，回放结果完全确定、可复现。

### 参数与两种补充速率

`Config` 的所有字段都必须为正整数，否则 `New` 返回 `ErrInvalidConfig`：

- `BaseRate`：基准补充速率（令牌 / 时间单位）。**时钟推进前队列为空**时使用，
  表示空闲期只按基准速率积累令牌。
- `CatchUpRate`：追赶补充速率（令牌 / 时间单位）。**时钟推进前队列有积压**时
  使用，允许积压事件以更快但仍受上限的速率追平，不会瞬时放水。
- `Capacity`：令牌桶容量，也是突发批量的拆平上限。初始桶为满；每次补充后令牌
  数以 `Capacity` 封顶，因此一次 `Drain` 最多吐出 `Capacity` 个事件。
- `MaxQueue`：FIFO 待发队列上限。入队会使队列长度超过该值时整体拒绝
  （`ErrQueueFull`）。

### 时钟、令牌与保序规则

- 逻辑时钟由 `Enqueue(now, event)` / `Drain(now)` 显式驱动，只能单调前进；
  `now < 当前时钟` 时返回 `ErrClockBackwards`。
- 每次推进经过 `dt = now - 当前时钟` 个时间单位，按推进**前**队列是否非空选择
  `CatchUpRate` 或 `BaseRate`，整数补充 `dt * rate` 个令牌并以 `Capacity` 封顶；
  同一时刻重复调用（`dt = 0`）不补充。
- 事件严格 FIFO：入队追加到队尾，`Drain` 从队首每事件扣 1 个令牌，直到令牌耗尽
  或队列清空，返回本步吐出的事件切片。
- 任何失败（时钟回退、非法参数、队列超限）都在修改前完成校验，整体拒绝，时钟、
  令牌、队列保持调用前状态。
- 并发安全：`Snapshot()` 在同一把读锁下返回 `State{Now, Tokens, QueueLen,
  TotalEnqueued, TotalDrained}`，并发读同一实例看到逐字段相同的一致快照；
  `SelfCheck()` 校验令牌范围、队列范围以及
  `累计入队数 == 累计吐出数 + 当前队列长度`。

可区分的错误（用 `errors.Is` 判断）：`ErrInvalidConfig`、`ErrClockBackwards`、
`ErrQueueFull`，见 `replay/errors.go`。

### 本地验证：逐步重放三元组

回放的正确性不靠“跑一遍看结果”，而是把场景拆成逐步重放三元组核对：

```
(操作 + 逻辑时间 now,  补充/扣减判定依据,  (时钟, 令牌, 吐出序列))
```

每一步只做一件事（在 `now` 入队一个事件，或在 `now` 吐出），手工按规则
预算本步结束后的三元组，再与 `Snapshot()` 和 `Drain` 返回值逐字段比对。
`TestStepwiseReplayTriplets` 就是该方法的可执行版本：

```bash
go test -race -v -run TestStepwiseReplayTriplets ./replay
```

日志会逐行打印操作、时钟、令牌、队列长度、累计入/出计数、吐出序列与判定依据。
其余关键场景：

```bash
# 追赶按 CatchUpRate 补充且被 Capacity 封顶
go test -race -v -run TestCatchUpRefillCapped ./replay
# 突发批量按桶容量拆平
go test -race -v -run TestBurstFlattenedByCapacity ./replay
# 队列空时按 BaseRate 补充
go test -race -v -run TestBaseRateWhenQueueIdle ./replay
# 队列超限 / 时钟回退整体拒绝且状态不变
go test -race -v -run 'TestQueueFullRejectedAtomically|TestClockBackwardsRejected' ./replay
# FIFO 保序与并发一致读
go test -race -v -run 'TestFIFOOrderingAcrossTicks|TestConcurrentReadsConsistent' ./replay

# 全量回归
go test -race -cover ./replay
```
