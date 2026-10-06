# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## `layout` 子系统

`layout/` 是编译器后端的复合类型内存布局与二进制兼容判定子系统，拆分为五个协作模块：

- 类型登记与引用（`registry.go`）：基本/复合类型登记、直接内嵌正反向边索引、并发控制；
- 布局计算（`layout.go`）：声明序放置、对齐、紧凑字段、`MaxAlign` 上限、空类型与尺寸上限；
- 版本与兼容判定（`compat.go`）：完全兼容 / 追加兼容 / 不兼容三态；
- 依赖与重算（`registry.go` 的 `Modify`）：只沿直接内嵌传播、完全兼容处停止、依赖者超限整体拒绝；
- 只读视图（`Registry.View`）：版本、大小、对齐、依赖者数量、累计重算次数同瞬一致。

间接引用大小/对齐由配置固定，允许未登记目标与经指针的环；直接内嵌环被拒绝。
所有操作在单把读写锁下串行化，查询返回同一瞬间的一致快照。
设计取舍与复杂度论证见 `layout/DESIGN.md`。

```bash
# 单元 + 随机差分（独立朴素模型对照）+ 并发（竞态检测）
go test -race -v ./layout
# 复杂度基准
go test -run NONE -bench . ./layout
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
