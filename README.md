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

## 有界队列：队首惰性过期 + 溢出策略（`boundedqueue` 包）

`boundedqueue.Queue[T]` 是泛型、并发安全（互斥锁，操作线性化）的有界队列，
容量 `C >= 1`，时钟由调用方以毫秒整数传入。

### 过期规则（队首惰性）

- 到期时刻 = 入队 `now + ttl`；`now >= deadline` 即视为已过期（恰等也过期）。
- 过期只在**队首**检查：从队首起连续移除已过期消息并记入死信（原因
  `expired`，按队列顺序），遇到第一条未过期消息立即停止。
- 其后哪怕已过期的消息仍留在队中并计入长度，直到它前面的消息全部离开。
- 入队仅在“长度已达 C”时先做队首清理；出队总是先清理队首再取队首。

### 溢出策略（构造时选定）

- `PolicyDropHead`：清理后仍满，则把当前队首（**不论是否过期**）移入死信，
  原因 `overflow`，然后入队。驱逐的是队首，不是后部已过期消息。
- `PolicyReject`：先**模拟**清理；模拟后仍满则整体拒绝（`ErrQueueFull`），
  模拟清理掉的过期消息仍留在队中，死信与 `maxNow` 均不变。模拟腾出空位则
  清理落地并入队，不触发溢出。

### 清理落地规则

- 拒绝策略下“清理后仍满”的入队：清理不落地。
- 出队清理后队空：返回 `ErrEmptyQueue`，清理同样不落地（过期消息仍在队）。
- 任何被拒绝的操作（容量非法、`ttl < 0`、时钟倒退、满、出队空）都不改变
  队列、死信与已见最大 `now`；时钟倒退（`now` 小于此前任一次调用的 `now`）
  的判定优先于其他原因（`errors.Is(err, ErrClockBackward)` 可区分）。

死信可通过 `DeadLetters()` 取有序快照，消息终态恰为
“在队中 / 已出队 / 死信(expired) / 死信(overflow)”之一；相同调用序列重放
得到完全相同的出队与死信序列。

### 本地验证

```bash
# 规则用例 + 与逐步朴素模拟的随机差分对照（含恰好一态与重放确定性）+ 竞态
go test -race -v ./boundedqueue/

# 多轮重复
go test -race -count=3 ./...

go vet ./...
gofmt -l .
```

注：若默认 `GOCACHE` 所在目录只读，可指定 `GOCACHE=/tmp/gocache`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
