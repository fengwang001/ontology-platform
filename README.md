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

## 有界队列（`queue` 包）

`queue` 包提供带队首惰性过期与溢出策略的有界队列，容量为 C（C ≥ 1），
时钟由调用方以毫秒整数传入。

### 队首惰性过期

- 入队时记录到期时刻 `expireAt = now + ttl`，`now >= expireAt` 即视为过期。
- 过期只在队首检查：清理从队首起连续移除已过期消息并记入死信
  （原因 `ReasonExpired`），遇到第一条未过期消息即停止；其后哪怕已过期
  的消息仍留在队中并计入长度。

### 溢出策略

入队时若长度等于 C，先模拟队首清理：

- 清理后有空位：清理落地，正常入队，不触发溢出。
- 清理后仍满，按构造时选定的策略处理：
  - `DropHead`（丢弃队首）：把队首（不论是否过期）移入死信
    （原因 `ReasonOverflow`），然后入队。
  - `Reject`（拒绝）：整体拒绝入队，返回 `ErrQueueFull`。

### 清理落地规则

- 清理只在操作被接受时落地；被拒绝的操作不改变队列、死信与已见最大 now。
- 拒绝策略下入队被拒、以及出队时清理后队空（`ErrEmptyQueue`），
  被模拟清理掉的过期消息仍留在队中、死信不变。
- 可区分的拒绝原因：`ErrInvalidCapacity`（C < 1）、`ErrNegativeTTL`
  （ttl 为负）、`ErrClockBackward`（now 倒退，优先于其他原因）、
  `ErrQueueFull`、`ErrEmptyQueue`。

### 并发与确定性

所有方法可并发调用，结果等价于某个串行顺序；长度含全部未被清理的
消息且不超过 C；每条消息恰好处于“在队中、已出队、死信（过期）、
死信（溢出）”之一；相同调用序列重放得到完全相同的出队与死信序列。

### 本地验证

```bash
# 规则覆盖 + 朴素模拟对照（日志含输入、输出与判定依据）
go test ./queue -run TestModelComparison -v

# 全量测试（含并发不变量）与竞态检测
go test -race -v ./queue
```
