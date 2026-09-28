# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 会话一致性令牌路由（`sessionroute`）

`sessionroute` 包把读写请求以会话为单位路由到“足够新”的只读副本，
保证同一会话能读到自己的写入（read-your-writes），且读到的进度单调不降。

### 令牌规则

- 主库维护一个从 0 开始、每次写入严格递增的序号；每个只读副本维护
  “已应用序号”，初始为 0；每个会话持有一个令牌，注册时为 0。
- 写入：主库序号加 1 产生新序号 `seq`，会话令牌取
  `max(当前令牌, seq)`，并返回 `{seq, 新令牌}`。
- 读取：只在已应用序号 `>= 令牌` 的副本中选择；读后令牌取
  `max(令牌, 被选副本观察进度)`。

### 路由规则

- 在所有满足“已应用序号不低于会话令牌”的副本中，选择已应用序号最小者；
  序号相同时按副本名字字典序选择（判定完全确定）。
- 不存在满足条件的副本时立即返回失败，不做等待、不做重试。
- “恰好追平”（副本已应用序号 == 令牌）即视为足够新，可以被选中。

### 推进规则（`Advance`）

- 只能把副本推进到不小于当前值的序号；小于当前值视为回退，拒绝。
- 推进序号不得超过主库当前最大序号；超过视为超前，拒绝。
- 推进到相同序号是幂等空操作，视为成功。

### 拒绝原因（可区分）

| Reason | 触发条件 |
| --- | --- |
| `unknown_session` | 读写引用了未注册的会话 |
| `unknown_replica` | 推进引用了不存在的副本 |
| `replica_behind` | 没有副本的已应用序号达到会话令牌 |
| `progress_rollback` | 推进序号小于副本当前进度 |
| `progress_ahead` | 推进序号大于主库当前序号 |

错误类型为 `*sessionroute.RejectError`，可通过其 `Reason` 字段区分。
任何被拒绝的操作都不会改变主库序号、副本进度或会话令牌。

### 并发与确定性

- 写入、读取、推进可被任意并发调用；内部用互斥锁保证每次操作原子可见。
- 并发下每个会话读到的进度单调不减，且不小于此前最后一次写入的序号
  （竞态检测测试 `TestConcurrentMonotonicPerSession` 覆盖）。
- 同一输入操作序列在全新路由器上反复重放，得到完全相同的输出
  （`TestDeterministicReplay` 覆盖）。
- 构造时传入一个 `io.Writer` 即可输出判定日志，其中包含每次操作的
  输入、所选副本、观察进度与判定依据（候选集合、取最小进度、
  字典序打破并列、令牌取 max 等）；传 `nil` 关闭日志。

### 用法示例

```go
router, _ := sessionroute.NewRouter([]string{"r-a", "r-b"}, os.Stdout)
router.RegisterSession("s1")

w, _ := router.Write("s1")      // 主库序号 1，s1 令牌变为 1
router.Advance("r-a", w.Sequence)
res, err := router.Read("s1")   // err == nil，路由到 r-a，res.Token == 1
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
