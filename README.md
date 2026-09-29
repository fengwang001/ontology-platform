# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 版本条件写入与删除墓碑

核心实现位于 `ontology` 包（`ontology/store.go`），用于处理可能乱序到达的变更事件。

### 规则

- **当前版本**：键的当前版本取存活行版本；无存活行则取墓碑版本；两者都没有则为 `0`。
- **条件写入**：事件仅当其版本 `event.Version` **严格大于**当前版本时才应用；否则忽略，`Ignored()` 计数加一。等版本事件一律忽略（写入不覆盖、删除不移除）。
  - 应用写入（`OpWrite`）：安装/替换存活行，并清除该键墓碑。
  - 应用删除（`OpDelete`）：移除存活行并安装墓碑；**键原本不存在也会建墓碑**，从而挡住删除后迟到的旧写入。
- **水位（watermark）**：所有已接受事件版本的全局最大值。被忽略的事件也参与水位（取 max）。
- **保留期与清扫**：每批事件应用后，凡满足 `watermark - tombstoneVersion >= RetentionVersions` 的墓碑立即被清除，该键回到无状态（当前版本 0、查询不存在）。`RetentionVersions = 0` 表示墓碑在同一批内即被清除；阈值是“差值达到保留参数”，即包含相等边界。
- **批量原子性**：`Apply([]Event)` 整批校验、整批提交；任何非法输入都在改动状态前拒绝，失败不留痕（存活行、墓碑、水位、忽略数均不变）。
- **条目上限**：`MaxEntries`（0 表示不限）限制存活行数。提交前按同样的版本规则投影批次后的存活行集合，超限整批拒绝；被忽略事件与删除不会计入增长，同批“先删后写”只要最终不超限即允许。

### 边界与错误类别

拒绝统一返回 `*ontology.BatchError`，三种互不相同的 `Reason` 可通过 `AsBatchError` 区分：

- `invalid_argument`：空批次（`len(events) == 0`）。
- `invalid_event`：事件键为空、版本 `<= 0`、或 `Op` 既非 `OpWrite` 也非 `OpDelete`；错误中带批次内下标。
- `limit_exceeded`：投影存活行数将超过 `MaxEntries`。

构造参数错误（`RetentionVersions < 0` 或 `MaxEntries < 0`）属于编程错误，`New` 直接 panic，快速失败。

与朴素参照（`ontology/reference.go`，墓碑永不清除）的一致性前提：

- 保留期大于任何可能版本（“永不清除”）时，两者存活行、墓碑、水位、忽略数逐步一致（见 `TestMatchesNaiveReferenceWithInfiniteRetention`，含随机乱序）。
- 有限保留期下，只要墓碑可能到期之后不再有版本不高于该墓碑的事件到达该键，过期对可观察状态不可见，仍与参照一致（见 `TestFiniteRetentionConsistencyUnderPrecondition`）。

### 并发

`Get` / `Rows` / `Tombstones` / `CurrentVersion` / `Watermark` / `Ignored` / `Verify` 使用读锁，可在另一执行体执行 `Apply`（写锁）期间被并发调用。快照按 key 排序返回。`Verify` 自检存活行与墓碑互斥、版本为正、墓碑不高于水位、无应清未清墓碑、行数不超限。

### 日志

`store.WithLogger(io.Writer)` 后，每个事件打印一行：输入（key/version/op/value）、判定时当前版本、判定依据（applied / ignored: live row / tombstone）；批次提交与墓碑到期也各打印一行，含存活行数、墓碑数、水位与忽略总数。测试通过 `t.Logf` 同步打印每步输入与存活行快照（`go test -v` 可见）。

### 用法示例

```go
store := ontology.New(ontology.Config{RetentionVersions: 10, MaxEntries: 1000}).
    WithLogger(os.Stdout)

err := store.Apply([]ontology.Event{
    {Key: "user:1", Version: 5, Op: ontology.OpWrite, Value: "alice"},
    {Key: "user:1", Version: 4, Op: ontology.OpDelete}, // 乱序旧事件，忽略
})
if be, ok := ontology.AsBatchError(err); ok {
    log.Fatalf("batch rejected: %s (%d)", be.Reason, be.Index)
}

row, exists := store.Get("user:1")
_ = store.Verify()
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
