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

## compactlog：带墓碑保留的压实日志

`compactlog` 包实现按键压实的日志，删除以墓碑形式保留一段时间，
使正在追赶的消费者仍能看到删除，而追赶完成的消费者视图始终正确。

### 写入与序号

- `Append(key, value)` 追加写入，`Delete(key)` 追加墓碑。
- 记录按追加顺序获得从 1 开始、单调递增的序号，压实后**不重编号**，只留空洞。
- 写入时间取自注入的 `Clock`（测试用 `ManualClock`，保证确定性）。

### 压实规则（`Compact`）

- 每个键只保留序号最大的最新记录，被后续同键记录覆盖的旧记录全部清除。
- 最新记录为墓碑时，仅当**写入时间距当前时间超过保留期**才清除；
  年龄恰好等于保留期的墓碑保留。
- 压实只删除记录、留下序号空洞，不改变任何存活记录的序号。

### 消费者读取规则

- `Subscribe()` 注册消费者，从序号 1 开始追赶；`Read(id, max)` 按序号升序
  应用至多 `max` 条记录，自动跳过空洞：写入记录设置视图键值，墓碑删除视图键。
- 新消费者追赶完成后的视图与对当前日志做朴素重放的结果一致。
- 写入、压实与多个消费者的读取可并发调用（内部互斥保护）。

### 可区分的拒绝原因

以下操作被拒绝且**不改变**时钟、序号计数器、日志或消费者位置与视图：

| 情形 | 错误 |
| --- | --- |
| 空键写入/删除 | `ErrEmptyKey` |
| 当前时间早于日志最大写入时间（写入或压实） | `ErrClockRegression` |
| 日志保留记录数达到容量上限 | `ErrLogFull` |
| 对未订阅的消费者读取/取视图 | `ErrUnknownConsumer` |

### 本地验证

```bash
# 全部测试（含竞态检测）
go test -race ./compactlog/

# 查看测试打印的输入序列、压实后日志、消费者视图及判定依据
go test -v ./compactlog/
```
