# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 广播状态规则版本化（`broadcast` 包）

`broadcast` 包实现广播状态（broadcast state）模式下的规则版本化处理：
规则变更先发布为不可变的全局版本，再按版本顺序逐个投递到各处理实例；
数据按 key 稳定路由到单一实例，并以“到达时全局已发布的规则版本”为标签。
每条数据的处理结果只取决于该标签版本，与各实例投递的快慢、穿插顺序无关，
因此结果可复现。

### 发布（Publish）

- 一次发布原子地应用一批变更（`OpUpsert` 新增/覆盖、`OpDelete` 删除），
  使全局版本加一；版本 0 为预置的空规则集。
- 发布只推进全局版本并保存该版本的**完整规则快照**，不会立即作用于任何实例。
- 删除一条当前不存在的规则是合法的；版本快照一经发布不可变。

### 投递（Deliver）

- 投递指定实例“下一个”版本：实例严格按 1,2,3,… 的顺序逐个生效，不可跳版本。
- 实例每应用完一个版本，**立即**按到达顺序处理缓冲区中标签等于该版本的数据，
  然后才允许投递再下一个版本；标签更大的数据继续留在缓冲区。
- 实例已生效全部已发布版本时再次投递返回 `ErrNoVersionToApply`（投递越界）。

### 数据路由（Send）

- 数据 key 必须非负；实例路由为确定性的 `instance = key % 实例数`，
  同一 key 永远落到同一实例。
- 数据到达时以当前全局版本打标签：实例已生效版本等于标签则立即处理；
  否则按到达顺序进入该实例的有界缓冲区等待刷出。
- 处理结果对每条命中阈值（`value >= Threshold`）的规则输出一条 `Hit`，
  同一数据的多条命中按规则标识（RuleID）排序；同一实例上按数据到达顺序排列。

### 缓冲刷出

- 每次版本生效后只刷出标签恰好等于该版本的缓冲数据，按到达顺序处理；
  其余数据保留。因此无论投递如何穿插，同一串发布与发送序列产生的最终命中
  集合相同、同一实例上的顺序相同。
- 若目标实例缓冲区已满（且标签不等于其已生效版本，即必须缓冲），
  `Send` 整体拒绝并返回 `ErrBufferFull`。

### 边界与错误类别

所有非法输入均**整体拒绝、失败不留痕**：全局版本、各实例已生效版本、
缓冲区、已有命中与发送序号都不改变。各原因互不相同，可用 `errors.Is` 判别：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidPublish` | 发布的变更批次为空 |
| `ErrInvalidRule` | 规则标识为空、操作类型未知等 |
| `ErrInvalidInstance` | 实例号越界，或构造时实例数/缓冲容量非正 |
| `ErrNoVersionToApply` | 投递越界：没有可投递的下一版本 |
| `ErrNegativeKey` | 数据 key 为负 |
| `ErrBufferFull` | 目标实例缓冲区已满 |

### 并发

发布、投递、发送与输出查询（`Hits` / `AllHits` / `AppliedVersion` /
`BufferLen` / `GlobalVersion`）均可被多个执行体并发调用，内部由互斥保护。
查询返回命中切片的副本，调用方可安全持有。

### 日志

引擎接受任何满足 `Info(msg string, args ...any)` 的日志器（`*slog.Logger`
天然满足），默认使用 `slog.Default()`。每步均记录输入、全局/已生效版本、
缓冲区长度、命中数量与判定依据（立即处理 / 进入缓冲 / 刷出 / 拒绝原因）。

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
go test -race -v ./broadcast

# 广播状态包：竞态检测 + 覆盖率
go test -race -coverprofile=/tmp/broadcast.cov ./broadcast
go tool cover -html=/tmp/broadcast.cov

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
