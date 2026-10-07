# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 属性索引一致性子系统

`ontology/ontology` 提供属性值与辅助索引的联合事务实现，支持：

- 单实例与批量属性写入的原子提交和失败回滚；
- 同一属性的多个索引结构同步维护；
- 对象删除与全部索引条目的同一事务清除；
- `Missing()` 与 `Explicit(default)` 的显式语义区分；
- 对象/属性/索引分级锁与查询串行等价；
- WAL、原子快照和 `begin/index/data/prepare/commit` 切分点恢复；
- 固定错误优先级、详细事件日志、朴素全表扫描模型随机对拍和竞态测试。

核心类型与方法：

- `New(schema, logger)` / `Open(schema, walPath, logger)`：创建或恢复平台；
- `WriteProperty(ctx, property, writes)`：同属性批量事务写入；
- `DeleteObject(ctx, id)`：删除对象并清除全部相关索引；
- `Query(indexName, property, key)`：只读取命中键桶的索引查询；
- `SetCrashHook(func(ctx, point) bool)`：注入崩溃切分点；
- `ErrObjectNotFound`、`ErrPropertyNotIndexed`、`ErrIndexMaintenance`、`ErrBatchRollback`。

设计取舍、放弃方案、恢复协议与复杂度证明见 [属性索引一致性设计说明](docs/property-index-consistency.md)。

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

# 属性索引对拍、恢复与竞态测试
GOCACHE=/tmp/go-cache go test -race -v ./...
GOCACHE=/tmp/go-cache go test -run TestRandomOperationsMatchNaiveFullScan -v ./...
```

如果默认 Go build cache 位于只读目录，请使用 `GOCACHE=/tmp/go-cache`。本仓库当前使用位于 `/usr/local/go/bin/go` 的 Go 工具链。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
