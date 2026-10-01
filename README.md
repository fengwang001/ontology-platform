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

## Plumtree 广播节点

核心实现位于 `plumtree/node.go`。

### 邻居状态迁移

- 构造后所有邻居位于 eager，lazy 为空。
- 首次收到某个 id 的 GOSSIP 时记录载荷、交付一次，清除该 id 的通告列表与截止时刻；完成转发后把来源加入 eager 并从 lazy 移除。
- 重复收到同一个 id 的 GOSSIP 时，不重复交付；把来源移出 eager 并加入 lazy，同时只向来源发送一条 PRUNE。来源已在 lazy 时也发送 PRUNE，集合保持不变。
- 收到 PRUNE 时把来源移入 lazy。
- 收到 GRAFT 或 Tick 触发嫁接时把来源移入 eager；GRAFT 对应已知 id 时立即回传该 id 的 GOSSIP，未知 id 时只迁移邻居状态。
- eager 与 lazy 始终互不相交；每次转发都先发 eager，再发 lazy，同组内按邻居标识升序输出，并排除当前消息来源。

### 通告、计时与嫁接

- 收到未知 id 的 IHAVE 时，把来源按调用到达顺序追加到该 id 的通告列表；同一来源重复 IHAVE 不重复入列。
- 某个 id 的首个有效 IHAVE 设置截止时刻 `now + T1`；GOSSIP 到达会删除该 id 的通告列表和截止时刻。
- Tick 只处理截止时刻小于等于当前时钟的 id，并按 id 升序处理。
- 每个到期 id 在一次 Tick 中只嫁接通告列表的第一个对端，即使当前时间已经跨过多个 T2，也不会补发多次。
- 嫁接后通告列表仍非空时，截止时刻更新为 `now + T2`；列表清空时删除计时器。计时器清空后再次收到 IHAVE，重新使用 T1。

### 错误优先级与原子性

构造参数按“自身为空、邻居为空、邻居为空串、邻居等于自身、邻居重复、T1 非正、T2 非正”的顺序报告第一个错误。

带状态入口按参数存在性依次检查：

1. `now` 小于此前任一次成功记录的调用时刻。
2. id 为空串。
3. from 不是邻居。
4. Broadcast 的 id 已经见过。

被拒绝的调用不会修改消息表、邻居集合、通告队列、计时器或最近时钟。节点内部使用互斥锁，使并发调用等价于某种合法串行顺序；返回的消息切片是新分配的，不与内部状态共享。

### 本地验证

如果 shell 找不到 `go`，可使用 `/usr/local/go/bin/go`；若默认构建缓存不可写，可指定临时缓存：

```bash
# 全量测试
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...

# 竞态检测
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...

# 查看确定性丢包模拟的输入、输出与判定日志
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -v -run TestLossyDeterministicSimulation ./plumtree

# 格式和静态检查
/usr/local/go/bin/gofmt -w plumtree/*.go
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go vet ./...
```
