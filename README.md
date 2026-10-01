# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## delivery：带预取上限的通道未确认投递账本

`delivery` 包实现通道级未确认投递账本：按投递标签批量确认 / 批量拒绝，被拒回队的消息按入队序号归位，未确认数从不超过预取上限 P。

### 标签（tag）与入队序号（seq）的区别

- **入队序号 seq**：`Enqueue` 时分配，从 1 起只增不复用，标识一条消息本身；队列始终按 seq 升序持有消息，投递时取 seq 最小者。
- **投递标签 tag**：`Deliver` 时分配，从 1 起每次投递加一（重投也分配新标签，旧标签随之结算失效），标识一次投递行为。确认与拒绝都按 tag 操作。
- 因此同一条消息回队后重投，seq 不变而 tag 更大；批量范围按 tag 取，与 seq 无关。

### 批量范围取法

- `Ack(t, multiple=false)` / `Reject(t, false, requeue)`：只结算标签恰好为 `t` 的那条未确认投递。
- `Ack(t, multiple=true)` / `Reject(t, true, requeue)`：结算当前所有标签不大于 `t` 的未确认投递（边界标签 `t` 含在内）。

### 回队归位规则

`Reject(..., requeue=true)` 时，范围内消息回到队列：每条插在入队序号更大的现存消息之前（按 seq 升序归位，不是简单放队首或队尾）；重投时 `Redelivered` 为真。`requeue=false` 时消息被丢弃并累加丢弃数。

### 拒绝原因（可区分的哨兵错误，且被拒绝的操作不改变任何状态）

- `ErrInvalidTag`：标签为 0 或大于已分配的最大标签。
- `ErrAlreadySettled`：非批量操作时该标签已结算（已确认 / 已丢弃 / 已回队）。
- `ErrEmptyRange`：批量操作时范围内没有任何未确认投递（标签合法但范围为空）。
- `ErrPrefetchFull`：投递时未确认数已达 P；与队列为空同时成立时优先报此错。
- `ErrQueueEmpty`：投递时队列为空（且预取未满）。

### 并发与确定性

所有方法（含 `Stats` / `Snapshot` 查询）均可并发调用，结果等价于某个串行顺序；任意时刻未确认数不超过 P；队列、未确认、已结算三者互不相交且并集等于全部入队消息；相同调用序列重放得到完全相同的投递序列、标签与重投标志。

### 本地验证

```bash
# 场景测试 + 与逐步朴素模拟的对照测试（日志打印输入、输出与判定依据）
go test -v ./delivery

# 并发线性一致性与不变量（竞态检测）
go test -race ./delivery
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
