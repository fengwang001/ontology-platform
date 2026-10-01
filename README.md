# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 层级主题通配订阅表（`topicstore`）

`topicstore` 包提供并发安全的 MQTT 风格层级主题订阅表，入口为 `topicstore.New()`。

### 主题与通配规则

- 主题与过滤器均为以 `/` 分隔的层级串，层级可为空串：`a//b` 为三层，`/a` 为两层且首层为空。
- `+` 匹配恰好一层（含空层），例如 `a/+/b` 匹配 `a//b`，但不匹配 `a/x/y/b`。
- `#` 只能是末层，匹配其父层及其下任意多层，含零层：`a/#` 匹配 `a`、`a/b`、`a/b/c`；单独的 `#` 匹配全部主题。
- `$` 主题例外：首层以 `$` 开头的主题（如 `$SYS/x`）不被首层为 `+` 或 `#` 的过滤器匹配，但被首层写明同一字面值的过滤器（如 `$SYS/#`）匹配。

### 订阅、退订与等级合并

- `Subscribe(clientID, filter, qos)`：等级仅允许 0、1、2；同一客户端重复订阅同一过滤器只覆盖等级，不新增条目。
- `Publish(topic, payload, retained)`：返回所有命中过滤器的客户端及授予等级；同一客户端被多个过滤器命中时只出现一次，等级取最大值，结果按客户端标识升序。
- `Unsubscribe(clientID, filter)`：退订指定过滤器；订阅不存在时返回 `ErrNoSubscription`。

### 保留消息

- `retained=true` 的发布按主题保存最后一条载荷；同主题再次保留发布即覆盖。
- 载荷为空串的保留发布会清除该主题的保留消息。
- 新订阅成功时，返回所有匹配该过滤器的保留消息，按主题字节序升序；`$` 例外同样适用。

### 拒绝原因（可区分，且拒绝不改变任何状态）

- `ErrInvalidFilter`：过滤器为空串、`#` 不在末层，或通配符与其他字符同层（如 `a+`）。
- `ErrInvalidTopic`：发布主题为空串或含 `+`/`#`。
- `ErrInvalidQoS`：等级不在 0–2。过滤器非法与等级越界同时成立时报告 `ErrInvalidFilter`。
- `ErrNoSubscription`：退订不存在的订阅。

### 并发与确定性

订阅、退订、发布、保留下发在同一把互斥锁下原子完成，因此并发调用等价于某个串行顺序；发布期间并发订阅的客户端要么完整看到该次发布，要么完全看不到。相同调用序列重放得到完全相同的匹配结果（见 `TestReplayDeterminism`）。

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
