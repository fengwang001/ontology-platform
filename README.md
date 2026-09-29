# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 数据血缘追踪（`lineage` 包）

`lineage.Tracker` 在对象派生操作发生时记录 `输入版本 → 输出版本` 的血缘，
支持上游（谁产生我）与下游（我产生谁）双向追溯，并保证血缘始终指向正确版本。

### 血缘记录

- `RegisterSource(ref)`：注册外部来源对象的某个版本（首次必须为 v1）。
- `RecordDerivation(Record{Operation, Inputs, Outputs})`：派生操作发生时原子记录血缘；
  每个 `输入 × 输出` 组合生成一条有向边，多输入多输出血缘完整不丢失。
- 记录前先做全部校验、通过后才写状态，因此**被拒绝的操作不改变任何血缘**。

### 追溯方向

- `Upstream(ref)`：沿 `输入 → 输出` 边反向遍历，回答“谁产生了我”。
- `Downstream(ref)`：正向遍历，回答“我产生了谁”。
- `Lineage(ref)`：同时返回双向血缘图；任一侧不完整即拒绝。
- 返回的 `Graph` 中 `Nodes`/`Edges` 均确定性排序，同一组派生无论以何种顺序
  （含并发交错）提交，得到的血缘图完全相同。
- 外部来源对象没有上游：`Upstream` 返回仅含根节点的空图（不报错）；
  派生对象若缺失生产血缘则返回 `ErrNoUpstream`。

### 版本与失效规则

- 对象版本严格单调递增：新对象从 v1 开始，后续派生只能产生 `当前版本+1`；
  派生输入必须引用输入对象的当前版本。
- 一旦对象产生新版本，所有引用该对象旧版本的血缘边立即标记为失效（`Edge.Invalid=true`），
  追溯遍历命中失效边即返回 `ErrStaleVersion`，保证血缘不会静默指向过期版本。

### 拒绝原因（可区分，互不相同的哨兵错误）

| 错误 | 触发场景 |
| --- | --- |
| `ErrInvalidRecord` | 派生无血缘记录：操作名为空、输入/输出为空、同一 ID 重复或同时作为输入与输出 |
| `ErrNoUpstream` | 查询的派生对象没有任何生产血缘（漏上游） |
| `ErrNoDownstream` | 查询对象没有任何消费血缘（漏下游） |
| `ErrStaleVersion` | 血缘引用旧版本，或试图用过期版本做派生输入/重复产出旧版本 |
| `ErrUnknownVersion` | 引用从未注册/产生过的对象或版本（含跳号、未来版本） |

### 并发

- 内部使用 `sync.RWMutex`：记录/注册为写操作，追溯为只读操作，可高并发共存。
- 校验与写入在同一把写锁内完成，并发派生不会出现半写入或丢失血缘。

### 日志

使用 `log/slog` 文本日志（`NewTrackerWithLogger(io.Writer)` 可重定向，`nil` 丢弃）：
- `derivation recorded`：打印操作名、全部输入/输出；
- `lineage query ok`：打印方向、根引用、边明细（血缘图）；
- `lineage query rejected` / `derivation rejected`：打印 `reason` 判定依据
  （`invalid_record` / `no_upstream` / `no_downstream` / `stale_version` / `unknown_version`）；
- `... invalidated ...`：版本推进导致旧血缘边失效的数量。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 仅血缘包，详细输出
go test -race -v ./lineage

# 覆盖率
go test -coverprofile=coverage.out ./lineage
go tool cover -func=coverage.out
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
