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

## 快照过期与数据文件清理（`snapshot` 包）

### 快照链

- `Table` 维护一条快照链：每次 `Commit(ts, added, removed)` 以
  **当前快照文件集 − removed + added** 产生新快照，并将其设为当前快照。
- 快照 ID 自 1 起严格递增；提交时间戳必须**严格递增**。
- 新增文件名**永不复用**：任何被历史提交使用过的名字（即使后来已被移除）
  都不得再次作为新增项。
- 移除项必须全部在当前快照文件集内。

### 保留条件与可删除文件判定

`Expire(keepLast, threshold)` 过期旧快照。一个现存快照被**保留**当且仅当
满足以下三者之一（取并集）：

1. 属于最新的 `keepLast` 个快照之一；
2. 其时间戳**严格大于** `threshold`；
3. 它是当前快照（当前快照永不过期）。

其余快照被过期移除。**可删除文件** = 仅被本次过期快照引用、且不被任何
保留快照引用的文件；实现上按引用计数判定：每个现存快照引用一次计一，
过期时递减，归零即从文件存储删除。`Expire` 返回被删除文件名（升序）。

不变式（`CheckConsistency` 自检）：每个现存快照引用的文件都在文件存储中，
且文件存储**恰为**全部现存快照文件集之并。因此保留快照始终可读、
无泄漏可复现。

### 边界与错误类别

所有非法输入**整体拒绝**，不改变快照链、引用计数与文件存储（失败不留痕）。
错误可用 `errors.Is` 区分，四种类别互不相同：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | 文件名为空、新增/移除列表内部重复、新增与移除相交、`keepLast < 0` |
| `ErrTimestampNotIncreasing` | 提交时间戳未严格大于上一次提交时间戳 |
| `ErrFileNameConflict` | 新增文件名已被历史使用过（文件名永不复用） |
| `ErrRemoveNotInSnapshot` | 移除项不在当前快照文件集内 |

边界说明：

- 首次提交接受任意时间戳；此后必须严格递增。
- 时间条件为**严格大于**：时间戳等于阈值的快照不被该条件保留。
- `keepLast = 0` 合法，表示不按“最新若干”保留；当前快照仍然保留。
- 全部方法（`Commit`/`Expire`/`Snapshots`/`Files`/`CheckConsistency`）
  均可被多个执行体并发调用，查询与自检可与提交、过期并发。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -count=1 ./...

# 查看逐步日志：每步输入、保留快照、删除文件与判定依据
go test -run TestAgainstNaiveModel -v ./snapshot/
go test -run TestExpireRetentionUnion -v ./snapshot/
```
