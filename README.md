# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 协作白板：叠放次序与编辑锁

- 生产实现：`internal/whiteboard`（implicit treap 全序、组合整体移动、TTL 软锁、乐观版本冲突）
- 朴素对照模型：`internal/naive`（切片线性实现，差分测试预言机）
- 随机差分测试：`internal/diff`（≥1500 条随机操作序列，日志在 `testlogs/diff.log`）
- 设计说明（关键取舍、放弃的方案、复杂度证据、本地验证）：`docs/DESIGN.md`

```bash
go test -race ./...
go test ./internal/diff/ -run TestRandomDifferential -v
go test -run '^$' -bench 'Benchmark(Rank|SingleReorder|LockUnlock)' -benchtime=100000x ./internal/whiteboard/
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
