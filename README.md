# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 层级主题通配订阅表（`pubsub`）

`pubsub.Table` 实现并发安全的 MQTT 风格层级主题订阅、通配匹配与保留消息。

### 主题与过滤器规则

- 主题与过滤器均以 `/` 分层，层级可为空串：`a//b` 为三层，`/a` 为两层且首层为空。
- 过滤器层级 `+` 匹配恰好一层（含空层），如 `a/+` 匹配 `a/`（末层为空）但不匹配 `a`。
- `#` 只能位于末层，匹配其父层及其下任意多层（含零层）：`a/#` 同时匹配 `a`、`a/b`、`a/b/c`；单独的 `#` 匹配全部主题。
- `$` 主题例外：首层以 `$` 开头的主题（如 `$SYS/x`）不被首层为 `+` 或 `#` 的过滤器匹配，但首层写明同一字面值的过滤器照常匹配，如 `$SYS/#` 匹配 `$SYS` 与 `$SYS/a/b`，`$SYS/+` 匹配 `$SYS/a`。

### 订阅与等级合并

- 订阅参数为（客户端标识, 过滤器, 等级），等级只允许 `0/1/2`。
- 同一客户端重复订阅同一过滤器只覆盖等级，不新增条目。
- 发布时同一客户端被多个过滤器命中只出现一次，授予等级取最大值，结果按客户端标识字节序升序。
- 退订不存在的（客户端, 过滤器）订阅会被拒绝。

### 保留消息

- 发布（主题, 载荷, 保留标志）时 `retain=true`：该主题只保留最后一条载荷；载荷为空串则清除该主题的保留消息（此前无保留时也不会新建条目）。
- 订阅成功时返回所有匹配该过滤器的保留消息，按主题字节序升序；`$` 例外同样适用。
- `retain=false` 的发布不影响保留消息。

### 错误（可通过 `errors.Is` 区分）

- `ErrInvalidFilter`：过滤器为空串、`#` 不在末层、通配符与其他字符同层（如 `a+`、`+x`、`a/#x`）。
- `ErrInvalidTopic`：发布主题为空串，或层级内含 `+`/`#`。
- `ErrInvalidQoS`：等级不在 `0..2`。
- `ErrNoSubscription`：退订了不存在的订阅。
- 过滤器非法与等级越界同时成立时优先返回 `ErrInvalidFilter`。
- 任何被拒绝的操作都不会改变订阅表或保留消息。

### 并发、确定性与日志

- 订阅、退订、发布、保留查询在同一把读写锁下完成，结果等价于某个全局串行顺序；发布期间并发的订阅要么完整看到该次发布、要么完全看不到。
- 所有对外结果按字节序排序，相同操作序列在新表上重放得到完全相同的匹配结果（见 `TestReplayDeterminism`）。
- 内部用 Trie 维护过滤器，匹配结果与逐层朴素参考实现 `naiveMatch` 在 4000 步随机序列上逐条对照（见 `TestDifferentialAgainstNaive`）。
- `NewTable(io.Writer)` 传入非 nil writer 时，日志打印每次操作的输入、输出与判定依据（命中的过滤器与各自等级、保留主题列表等）；生产代码可直接 `t.Log`/文件等任意 writer。

### 本地验证

```bash
# 若 go 不在 PATH：export PATH=$PATH:/usr/local/go/bin
go test ./...
go test -race -count=5 ./...
go test -run TestDifferentialAgainstNaive -v ./pubsub
go vet ./...
gofmt -l .
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
