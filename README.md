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

## 两表外键内连接组件（`fkjoin`）

`fkjoin` 包维护左表（每行一个可空外键）与右表的内连接结果。
左右两侧通过**订阅**与**FIFO 响应队列**异步通信，结果在两侧交错变更、
响应乱序（过期）投递的场景下始终与朴素内连接一致。

### 数据模型

- 左表：`map[左行ID]Row{FK *string, Value string}`，`FK == nil` 表示外键为空。
- 右表：`map[键]值`。
- 结果：外键非空且右表存在对应键时，结果为 `{左行ID, 键, 左值, 右值}`，
  否则该左行不在结果中。`NaiveJoin` 提供朴素内连接作为判定基准。

### 订阅规则

- `UpsertLeft` 外键非空时建立订阅（左行 ID → 外键）；外键改为空或改为其他键时
  撤销原订阅并立即撤回对应结果。
- `DeleteLeft` 删除左行并撤销订阅、撤回结果。
- `DeleteRight` 删除右表键时立即撤回所有引用该键的结果；订阅保留，
  右表键恢复并经响应确认后结果重新出现。
- 右表侧驱动通过 `Lookup(key)` 查询某键的订阅者及其当前左行哈希，
  据此产生响应并调用 `Enqueue` 入队。

### 响应与投递规则

- 响应按产生顺序进入 FIFO 队列（`Enqueue` 分配单调递增序号），
  `Deliver` 严格按序投递队首响应，`Drain` 排空全部响应。
- 投递时依次校验：左行存在 → 左行哈希一致 → 右表当前仍持有响应产生时的键值。
  全部通过则更新结果；任一失败按过期响应**丢弃并计数**（`Discarded()`），
  判定依据记录在 `Delivery.Evidence` 与日志中。
- 左行哈希（`HashRow`）由左行 ID、外键与左值决定：左行任何变更都会使
  在途旧响应的哈希校验失败，从而被安全丢弃。

### 拒绝规则（可区分原因，拒绝无任何副作用）

| 场景 | 原因 |
| --- | --- |
| 外键/右表键为空字符串 | `ErrEmptyKey` |
| 对空响应队列执行 `Deliver` | `ErrEmptyQueue` |
| 待投递响应达到上限 | `ErrPendingLimit` |

被拒绝的操作不会改变两表、订阅、队列、结果与丢弃数。
错误以 `RejectError`（含操作名与原因）返回，可用 `errors.Is/As` 判定。
注意：外键为 `nil`（空外键）是合法的，仅空字符串外键被拒绝。

### 一致性与并发

- 所有方法可并发调用；`Results()` 返回按左行 ID 排序的快照，逐条一致。
- 排空全部响应后，`Results()` 与 `NaiveJoin(Left(), Right())` 完全一致。
- 同一输入序列反复计算得到完全相同的结果、丢弃数与日志输出
  （日志带单调序号，无时间戳）。

### 本地验证

```bash
# 运行 fkjoin 全部测试（含竞态检测）
go test -race -v ./fkjoin/

# 覆盖率
go test -coverprofile=coverage.out ./fkjoin/
go tool cover -func=coverage.out
```

测试覆盖：哈希校验丢弃过期响应、外键改为空、删除右表键撤回结果、
空键/空队列/超限等非法输入、交错变更与乱序响应下的最终一致性、
并发读取一致性、FIFO 投递顺序与确定性重放。
