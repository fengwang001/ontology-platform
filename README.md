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

## 并行对齐快照点导出器（`snapshot` 包）

跨多个分区导出全局一致快照：每个分区独立投递事件、同分区序号从 1 连续递增。

### 对齐点规则

- 对齐点 = 所有分区已投递事件数的**最小值**。
- 快照只纳入每个分区的**前对齐点条**事件，按分区顺序拼接；
  用 `Snapshot.Materialize()` 物化为键值视图时，同键跨分区**后写覆盖前写**。
- 任一快照内每个分区贡献的条数都恰等于该快照的对齐点，
  不存在半个分区被纳入的中间态（快照、自检与投递可并发，`sync.RWMutex` 保护）。

### 背压上限规则

- 某分区超出当前对齐点的事件进入**待定缓冲**，每个分区的待定缓冲受
  `NewExporter(numPartitions, pendingLimit)` 的上限约束。
- 投递后待定缓冲将超出上限时，该事件被**整体拒绝**且各分区事件数与待定缓冲均不变；
  其他分区补齐、对齐点推进后，待定缓冲释放，可继续投递。
- 拒绝原因可区分（`errors.Is` 判定）：
  `ErrPartitionOutOfRange`（分区号越界）、`ErrEmptyKey`（空键）、
  `ErrBackpressure`（背压超限）。

### 本地验证方法

用“取各分区前缀拼接”核对快照结果：

1. `counts := e.Counts()` 取各分区事件数，对齐点 `align = min(counts)`。
2. 对每个分区取前 `align` 条，按分区号升序拼接，应与 `e.Snapshot().Events` 完全一致。
3. 依次将拼接结果应用到空 map（后写覆盖前写），应与 `Snapshot().Materialize()` 一致。
4. 调用 `e.SelfCheck()` 校验序号连续、对齐点取最小值、各分区贡献一致。

```bash
# 带竞态检测与详细日志（含投递、各分区事件数、对齐点、待定缓冲与判定依据）
go test -race -v ./snapshot/
```
