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

## 未确认投递账本（`ledger` 包）

`ledger.Ledger[T]` 实现带预取上限 P（P ≥ 1）的通道未确认投递账本，支持按投递标签的批量确认 / 批量拒绝，被拒回队消息按入队序号精确归位。所有方法可并发调用，内部用互斥锁保证等价于某个串行顺序。

### 两个序号，切勿混淆

- **入队序号 Seq**：消息入队时分配，从 1 起，只增不复用；回队、重投都不变。它决定队列顺序（队列始终按 Seq 升序持有消息）。
- **投递标签 Tag**：每次 `Deliver` 分配一个，从 1 起，每次投递加一；**重投也分配全新且更大的标签**。确认 / 拒绝的批量范围只按 Tag 取，与 Seq 无关。

### 操作语义

- `Enqueue(msg) Seq`：分配下一个入队序号并放入队列。
- `Deliver() (Delivery, error)`：仅当未确认数 < P 时，取队列中 Seq 最小者，分配新 Tag；首次投递 `Redeliver=false`，回队后再次投递为 `true`。
- `Ack(tag, multiple)`：`multiple=false` 只确认 `tag`；`multiple=true` 确认当前所有 `Tag ≤ tag` 的**未确认**投递。被确认者永久移除（结算）。
- `Nack(tag, multiple, requeue)`：范围取法同 `Ack`；`requeue=true` 回队，`requeue=false` 丢弃并累加丢弃数。

### 回队归位规则

回队消息不是放到队首或队尾，而是**按入队序号升序归位**：插在所有现存的、Seq 更大的队列消息之前。因此回队消息与一直未投递的消息混排后，重投顺序仍严格按 Seq 升序；重投只更换 Tag 并置 `Redeliver=true`，Seq 保持不变。

### 被整体拒绝的操作（可区分原因）

错误类型为 `*ledger.Error`，其 `Reason` 字段取下列值；校验先于任何状态变更，被拒绝时队列、Tag 计数、未确认集合与丢弃数均不变：

- `ErrTagIllegal`：Tag 为 0 或大于已分配的最大标签。
- `ErrAlreadySettled`：非批量时该 Tag 已结算（已确认、已丢弃或已回队——回队后旧 Tag 立即失效）。
- `ErrRangeEmpty`：批量时 Tag 合法，但 `[1, tag]` 内没有任何未确认投递。
- `ErrPrefetchFull`：投递时未确认数已等于 P。
- `ErrQueueEmpty`：投递时未确认数未满，但队列为空。

**优先关系**：预取已满与队列为空同时成立时，报 `ErrPrefetchFull`（实现中先判满再判空）。

### 不变量

- 任意时刻未确认数 ≤ P。
- 队列消息、未确认消息、已结算（已确认或已丢弃）消息三者互不相交，并集等于全部入队消息。
- 相同的调用序列在新账本上重放，得到完全相同的投递序列、Tag 与 `Redeliver` 标志。

### 本地验证

```bash
# 全量测试（含与朴素模型的随机差分、并发线性化、重放确定性）
go test ./ledger

# 竞态检测
go test -race ./ledger

# 查看输入 / 输出 / 判定依据的逐步日志
go test -v -run TestScenarioAgainstNaiveModel ./ledger
```

测试要点：批量确认含边界标签；批量拒绝回队后按 Seq 归位且早于更大 Seq 的现存消息；回队消息重投 Tag 大于未回队者，但批量确认仍按 Tag 取范围；重投标志；预取恰好放下与恰好放满；五类拒绝原因及“预取已满优先于队列为空”；随机操作流（40 个种子 × 每种子 2000 步）与按规则直接编写的朴素模型（`model_test.go`）逐步对照。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
