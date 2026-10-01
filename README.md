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

## Plumtree 广播树节点

`plumtree.go` 实现单节点状态机，消息统一为 `(Target, Type, ID, Payload)`，类型包括 `GOSSIP`、`IHAVE`、`PRUNE`、`GRAFT`。

### Eager 与 Lazy 迁移

- 构造后所有邻居都在 eager，lazy 为空；每个节点独立保存 eager/lazy 集合。
- `Broadcast` 先按邻居标识升序向 eager 发送 `GOSSIP`，再向 lazy 发送 `IHAVE`。
- 新 `GOSSIP` 到达：清除该消息 id 的通告队列和计时器，先转发给除来源外的 eager，再通告给除来源外的 lazy，最后把来源放入 eager 并从 lazy 移除。
- 重复 `GOSSIP` 到达：不再次交付，把来源移出 eager 并放入 lazy，返回一条发给来源的 `PRUNE`；来源原本已在 lazy 时集合不变，但仍返回 `PRUNE`。
- `OnPrune` 把来源从 eager 移到 lazy；`OnGraft` 把来源从 lazy 移到 eager。
- `OnGraft` 的消息 id 已见时额外回传带已记录载荷的 `GOSSIP`；未见时只完成迁移且无输出。

### 通告计时与嫁接次序

- `OnIHave` 遇到未见 id 时，把来源追加到该 id 的通告队列；同一来源重复通告不重复入列，已见 id 直接忽略。
- 队列从无计时器时，截止时刻为 `now + T1`；`Tick` 嫁接队首后如果队列仍非空，下一次截止为当前 `Tick(now)` 的 `now + T2`。
- `Tick` 只处理 `deadline <= now` 的 id，按 id 升序，每个 id 每次最多处理一次；即使 `now` 已跨过多个 T2，也只嫁接队首一次。
- 嫁接时把队首来源移入 eager，输出 `GRAFT(target, id)`；队列为空后删除计时器，之后新的 `IHAVE` 重新使用 T1。
- 新 `GOSSIP` 交付会删除对应通告队列和计时器，因此不会再为该消息嫁接。

### 错误优先级与原子性

构造参数按自身标识、邻居集合、T1、T2 的顺序校验：自身不能为空；邻居集合非空；邻居标识不能为空且互不相同；邻居不能包含自身；T1、T2 必须为正整数。

各入口只校验其拥有的参数，并按以下顺序返回第一个错误：

1. `now` 小于此前任一次成功入口调用的时间：`ErrClockMovedBack`。
2. id 为空：`ErrEmptyID`。
3. `from` 不在邻居集合：`ErrUnknownNeighbor`。
4. `Broadcast` 的 id 已见过：`ErrAlreadyDelivered`。

被拒绝的入口不更新最近时钟、消息表、eager/lazy、通告队列或计时器。所有入口共用互斥锁，并发结果等价于某个合法串行顺序；每次返回的消息切片都是新分配的，不与内部状态共享。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看确定性丢包模拟的输入、输出、判定依据与最终计数
go test -run TestDeterministicLossyBroadcastReplay -v

go vet ./...
```

该模拟使用 4 个全连接节点，确定性注入两条丢失的 `GOSSIP`。节点先通过重复 `GOSSIP`/`PRUNE` 剪枝，再经 `IHAVE` 超时和 `GRAFT` 修复，最终 4 个节点都交付两条消息；模拟同时重放两次并比较完整日志，保证消息序列逐条一致。
