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

## 两表外键内连接组件（`ontology/fkjoin`）

`ontology/fkjoin` 维护左表与右表的外键内连接结果，支持左右两表交错变更、
响应乱序投递，结果始终正确。

### 连接规则

- 左行带可空外键（`[]byte`，`nil` 表示空）。外键非空且右表存在对应键时，
  结果为一条 `Row{LeftID, LeftValue, RightKey, RightValue}`；否则不在结果中。
- 结果只通过响应投递维护；未排空的在途响应不影响已可见结果（最终一致）。
- 排空全部响应后，`Snapshot()` 与 `NaiveJoin()`（按当前两表状态的朴素内连接）
  逐条一致；同一输入序列重复执行得到完全相同的输出（扇出按左行 ID 升序入队）。

### 订阅与响应

- 非空外键的左行即为该键的订阅者；`UpsertLeft` 非空外键时入队一个
  `lookup` 响应（携带右表当前是否命中）。
- `PutRight` / `RemoveRight` 向所有订阅该键的左行按 FIFO 扇出
  `put` / `remove` 响应。响应按产生顺序进入一条全局先进先出队列，
  用 `DeliverOne()` 逐条投递或 `Drain()` 排空。
- 外键置空或删除左行会立即撤回结果并取消订阅；取消前已在途的响应投递时丢弃。

### 哈希校验与丢弃

- 每个响应记录产生时左行的 SHA-256 哈希（覆盖左行 ID、值、外键）。
- 投递时重新校验：左行已删除按 `left-row-gone` 丢弃；哈希不一致按
  `hash-mismatch` 丢弃。丢弃不修改结果，但计入 `Dropped()`。
- 命中的响应写入/更新结果；右表缺失的响应撤回该左行的结果。

### 拒绝规则（可区分原因，且无任何副作用）

所有拒绝都在状态变更前判定，两表、订阅、队列、结果、丢弃数均保持不变：

- `ErrNullKey`：左行 ID 为空、外键为非 nil 的空切片，或右表键为
  nil/空切片。（注意：外键 `nil` 是合法的“空外键”，表示不订阅。）
- `ErrEmptyQueue`：队列空时调用 `DeliverOne()` 或 `Drain()`。
- `ErrQueueOverflow`：操作将产生的响应数会使待投递队列超过
  `New(maxPending, logger)` 配置的上限（左表单条、右表按订阅者扇出计数）。

### 并发与一致性

- 内部使用 `sync.RWMutex`，变更/投递与 `Snapshot()`、`Pending()`、
  `Dropped()` 可并发；快照在锁内拷贝生成，逐条一致且与内部状态隔离。
- 传入 `New` 的 `Logger` 会收到输入（`input ...`）、输出（`output join/
  withdraw/discard`）条目及判定依据（`reason`、`cause`、`hit` 等）。

### 本地验证

```bash
# 竞态检测 + 详细日志
go test -race -v ./ontology/fkjoin

# 覆盖率
go test -cover ./ontology/fkjoin

# 全量检查
go test -race ./...
go vet ./...
gofmt -l .
```
