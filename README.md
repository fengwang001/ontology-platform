# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## compactlog：带删除墓碑保留的压实日志

`compactlog` 包实现一个并发安全的按键压实日志，供多消费者按序号追赶读取。

### 写入与序号

- `Append(key, value, tombstone, ts)` 追加一条记录；记录按追加顺序获得单调递增、**永不重编号**的序号（从 1 开始）。
- `tombstone=true` 表示删除墓碑（`value` 被忽略），用于向尚未追平的消费者传播删除。
- 以下写入会被拒绝并返回可区分的错误，且**不改变时钟、序号计数器、日志或任何消费者状态**：
  - 空键：`ErrEmptyKey`
  - 写入时间早于上一条成功写入（时间回退）：`ErrTimeRegression`
  - 日志中记录数达到容量上限：`ErrLogFull`

### 压实与墓碑保留

- `Compact(now)` 压实日志，只在序号序列中留下空洞，不重编号：
  - 每个键只保留序号最大的最新记录，被后续同键记录覆盖的旧记录全部删除；
  - 写入时间距今**超过**保留期（`now - WrittenAt > retention`）的墓碑被清除；恰好达到保留期的墓碑仍保留；
  - 被清除的墓碑所覆盖的旧记录已被压实删除，因此清除过期墓碑不影响新消费者的最终视图。
- `now` 早于最后写入时间时拒绝（`ErrTimeRegression`），日志不变。

### 消费者读取

- `Subscribe(name)` 注册从日志起点追赶的消费者；重复订阅返回 `ErrConsumerExists`。
- `ReadNext(name)` 按序号顺序返回下一条记录，自动跳过压实留下的空洞，并把记录应用到该消费者的物化视图（墓碑删除对应键）；追平时返回 `ok=false`。
- 对未订阅的消费者读取或取视图返回 `ErrUnknownConsumer`，且不改变任何状态。
- 新消费者从压实后的日志追赶，其视图与对完整历史做朴素重放完全一致。
- 写入、压实与不同消费者的读取可并发调用（内部互斥锁保护）；时间戳由调用方显式提供，同一输入序列反复计算得到完全相同的输出。

### 本地验证

```bash
go test -race -v ./compactlog/
```

测试日志会打印输入序列、压实后日志、各消费者视图及判定依据，覆盖：墓碑恰好达到保留期、压实顺序与空洞、各类非法输入、并发读写压实、确定性重算。

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
