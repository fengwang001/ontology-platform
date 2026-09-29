# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`dedup`](dedup/README.md)：多源并集的增量去重维护器，按分区引用计数维护去重并集视图，
  元素仅在所有分区都撤回后才撤下，增量结果与批量重算一致；并发安全并提供变更日志与自检。

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
