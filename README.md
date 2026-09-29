# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## compactlog：带墓碑保留的按键压实日志

`compactlog` 包实现了一个可并发使用的压实日志，让正在追赶的消费者能看到删除，
而已追平的消费者视图始终正确。

### 写入与序号

- `Write(key, value, now)` 追加记录；`value == nil` 表示删除该键（写入墓碑）。
- 记录按追加顺序获得从 1 开始单调递增的序号，压实只留空洞，序号永不重排、不复用。
- 拒绝规则（返回 `*compactlog.Error`，可用 `errors.As` 取出 `Reason` 区分）：
  - `ReasonEmptyKey`：空键；
  - `ReasonClockRegression`：`now` 早于上次成功写入时间；
  - `ReasonLogFull`：存活记录数达到容量上限；
  - `ReasonConsumerNotSubscribed`：未订阅的消费者调用 `ReadNext`/`View`。
- 被拒绝的操作不改变时钟、序号计数器、日志内容以及任何消费者的位置与视图。

### 压实与墓碑保留

- `Compact(now)` 对每个键只保留序号最大的记录，删除所有被后续同键记录覆盖的旧记录。
- 若某键最新记录是墓碑，且 `now - 写入时间 > retention`（超过保留期），墓碑一并清除；
  恰好达到保留期（`now - 写入时间 == retention`）的墓碑仍然保留，保证正在追赶的
  消费者仍能观察到这次删除。

### 消费者读取

- `Subscribe(name)` 注册从头读取的消费者；`ReadNext(name)` 按序号升序返回其位置之后
  的下一条存活记录（自动跳过空洞）并应用到该消费者自己的视图；返回 `ok=false`
  表示已追平。`View(name)` 返回当前物化视图副本。
- 墓碑在应用时表现为从视图中删除该键；新消费者读到的视图与对存活记录做朴素重放
  的结果完全一致。
- 所有方法共用一把互斥锁，写入、压实与多个消费者的读取可安全并发调用；
  同一输入序列反复计算得到完全相同的输出。

### 本地验证

```bash
# 全部测试（含墓碑边界、压实顺序、非法输入、并发、确定性）
go test -race -v ./compactlog
```

测试日志会打印每条输入、压实后的日志内容、各消费者视图以及判定依据。

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
