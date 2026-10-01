# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 死信重放器

核心实现在 `replay` 包中：

```go
broker, err := replay.NewBroker(capacity, maxReplays, "orders", "payments")

oseq, err := broker.Enqueue("orders", "message payload")
message, err := broker.Dequeue("orders")
dseq, err := broker.SendToDeadLetter("orders")
count, err := broker.Replay("orders", 10)
queues, dead := broker.Snapshot()
```

每条消息的 `OSeq` 是全局入队序号，从 1 开始且终身不变；每次成功进入死信区都会分配新的全局死亡序号 `DSeq`，也从 1 开始。消息重新进入队列后再被送入死信区时，会获得新的 `DSeq`，但 `OSeq` 与累计 `Replays` 不变。

### 重放顺序与次数

- `Replay(queue, n)` 只查看死信区中属于目标队列、且 `Replays < R` 的条目。
- 可重放条目按 `OSeq` 升序处理，而不是按 `DSeq` 死亡序处理。
- 已达到 `Replays == R` 的条目留在死信区并被跳过，不占用 `n` 的名额。
- 每成功放回一条，该消息的累计重放次数加一，并从死信区移到目标队列队尾。
- 处理持续到已放回 `n` 条，或目标队列达到容量 `C`。

### 成功、部分成功与拒绝

- 至少成功放回一条时返回 `(放回条数, nil)`；容量中途耗尽不回滚已放回消息，剩余条目留在死信区。
- 一条也放不回时整体拒绝，所有队列内容、死信区内容和重放次数保持不变。
- 拒绝原因按以下优先级判断：`n < 1` 或队列名为空；队列不存在；死信区没有该队列条目；该队列条目全部达到重放上限；目标队列已满。
- 对应的哨兵错误为 `ErrInvalidArgument`、`ErrQueueNotFound`、`ErrNoDeadEntries`、`ErrReplayLimitReached` 和 `ErrQueueFull`。

所有公开方法使用同一把互斥保护状态，并发调用的结果等价于某个串行执行顺序。任意时刻，每条未被 `Dequeue` 移除的消息都恰好位于一个命名队列或死信区中；相同调用序列产生确定的 `OSeq`、`DSeq`、队列内容和死信区内容。

## 在代码中使用

```go
import "ontology/replay"
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 死信重放器测试（含逐步输入、输出与判定依据日志）
go test -race -v ./replay

# 单个用例
go test -run TestReplayRulesAgainstNaiveSimulation -v ./replay

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`TestReplayRulesAgainstNaiveSimulation` 使用独立编写的逐步朴素模拟逐操作对照，覆盖按 `OSeq` 而非 `DSeq` 重放、达到上限跳过且不占名额、跳过后继续处理后续条目、容量中途耗尽的部分成功、零进展时报满、再次死信后的身份与累计次数保持，以及全部拒绝优先级。

如果当前 shell 找不到 Go 工具链，可用 `/usr/local/go/bin/go` 替换上述 `go`；若默认构建缓存只读，可先执行 `export GOCACHE=/tmp/go-cache-ontology`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
