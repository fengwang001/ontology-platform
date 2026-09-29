# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 键控惰性模式迁移

`migration` 包提供泛型键值存储 `Store[K]`。模式升级只提高当前版本号，不扫描、不重写任何键；旧版本数据在下一次读取该键时才迁移。

### 迁移链

- 迁移函数用 `RegisterMigration(fromVersion, fn)` 按来源版本登记，每个来源版本最多登记一次。
- 从存储版本 `s` 读到当前版本 `c` 时，只执行 `s -> s+1`、`s+1 -> s+2`、一直到 `c-1 -> c` 的步骤。
- 读取前先检查完整连续链；只要缺少任意一步，立即返回缺迁移错误，并且不调用任何迁移函数。
- 跨版本结果等价于从原始版本开始按顺序运行朴素迁移链，因此升级顺序不会改变最终字节结果。

### 写回与去重

- 迁移开始前复制原始数据，迁移函数收到的输入与调用方返回值互不共享底层字节。
- 整条链全部成功后才在临界区写回新版本和数据；任一步失败时保留原版本、原数据。
- 写回前重新检查键的存储版本和原始快照；若该键在迁移期间被其他写入替换，则不覆盖新状态，本次仍返回刚计算出的结果。
- 同一键的并发读取共用同一个飞行迁移任务，因此迁移链只执行一次，所有读取者拿到逐字节一致的结果。
- 成功写回后，后续读取直接命中当前版本，迁移步骤不会重复执行；不同键的迁移可以并发进行。
- `Upgrade` 等待飞行迁移完成；普通 `Write` 也等待同键飞行迁移完成，然后用当前版本覆盖该键。

### 边界与错误类别

错误均可使用 `errors.Is` 区分：

- `ErrInvalidArgument`：版本为负数、版本未递增、重复登记、迁移函数为空，或存储版本高于当前版本。
- `ErrNotFound`：读取或查询不存在的键。
- `ErrMissingMigration`：迁移链缺少连续来源版本；此时所有已登记函数零调用。
- `ErrMigrationFailed`：某个迁移函数返回错误或 `nil`；存储、当前版本和已登记迁移均保持不变。

任何被拒绝的登记、升级、写入或读取都不会产生部分修改。可向 `NewStore` 传入 `Logger`，日志会记录每步输入、函数返回、失败或成功判定、写回判定，以及缺迁移时的零调用判定。

### 用法示例

```go
store, err := migration.NewStore[string](0, logger)
if err != nil {
    return err
}

if err := store.RegisterMigration(0, func(value []byte) ([]byte, error) {
    return migrateV0ToV1(value), nil
}); err != nil {
    return err
}

if err := store.Write("object:1", original); err != nil {
    return err
}
if err := store.Upgrade(1); err != nil {
    return err
}

current, err := store.Read("object:1") // 惰性迁移并原子写回
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

# 迁移包：常规与竞态验证
go test -race -run 'TestMultiVersionMigration|TestConcurrent' ./migration

# 如果 HOME 中的 Go 缓存目录只读，可显式指定临时缓存
GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/go-modcache go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
