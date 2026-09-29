# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 逻辑删除（软删）组件

`softdelete` 包（`softdelete/store.go`）提供并发安全的软删能力，对象在状态机
`alive ⇄ deleted → purged` 之间迁移：

- **软删只打标记**：`SoftDelete` 将对象置为 `deleted`，记录物理保留、值不丢失，
  并通过 `Revision` 单调递增记录迁移次数。
- **默认查询过滤**：`Get`/`List` 默认只返回存活对象；传入
  `QueryOptions{IncludeDeleted: true}` 可显式包含软删对象。
- **唯一键占用规则**：软删对象仍然占用唯一键，软删后用同键 `Create` 会被拒绝
  （`key_occupied`）；只有 `Purge` 物理删除才释放键，物理删除后同键可重新创建。
- **复活规则**：`Restore` 撤销删除标记并恢复默认查询可见；复活对象继续占用唯一键。

### 拒绝原因（可区分，且拒绝不改变任何状态）

| 场景 | 原因（`Reason`） |
| --- | --- |
| 软删从未存在或已物理删除的对象 | `not_found` |
| 软删一个已经软删的对象 | `already_deleted` |
| 复活一个存活（未软删）的对象 | `not_deleted` |
| 物理删除不存在的对象 | `not_found` |
| 创建时唯一键被存活/软删对象占用 | `key_occupied` |

错误可用 `errors.Is(err, softdelete.ErrKeyOccupied)` 判定，或用
`softdelete.ReasonOf(err)` 取出原因；错误本体为 `*softdelete.OpError`。

### 并发语义

所有状态迁移在互斥锁内以“检查—迁移”原子方式完成，不暴露任何中间态。重复的同语义
操作（如多个并发 `SoftDelete`）恰好一次生效，其余以可区分原因拒绝；可交换操作组
（如 `SoftDelete + Purge`、`Restore + Purge`）以任意顺序执行得到相同最终状态。
冲突的 `SoftDelete` 与 `Restore` 并发时遵循“最后提交者胜”，双方要么迁移成功要么
被明确拒绝，状态始终合法。

每次判定均通过 `slog` 记录 `op`、`key`、`object_state`、`decision`、`reason`
与 `basis`（判定依据）；可用 `NewWithLogger` 注入自定义 logger。

### 使用示例

```go
store := softdelete.New[string, *User]()
ctx := context.Background()

if _, err := store.Create(ctx, "user-1", user); err != nil { /* key_occupied 等 */ }
_ = store.SoftDelete(ctx, "user-1")                 // 打删除标记，记录保留
_, _ = store.Create(ctx, "user-1", other)           // 拒绝：key_occupied
_, _ = store.Get(ctx, "user-1", softdelete.QueryOptions{})              // 不可见
got, _ := store.Get(ctx, "user-1", softdelete.QueryOptions{IncludeDeleted: true}) // 可见
_ = store.Restore(ctx, "user-1")                   // 复活，恢复可见
_ = store.Purge(ctx, "user-1")                     // 物理删除，释放唯一键
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
go test -race -v ./softdelete
go test -run TestSoftDeletedKeyOccupiedUntilPurge ./softdelete

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
