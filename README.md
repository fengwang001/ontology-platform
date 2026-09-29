# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`router/`](router/README.md)：分区保序路由器。按分区位点（从 0 连续递增）
  严格保序接受变更事件，维护各分区计数/和值、全局总和，以及取所有分区最小
  对齐前缀的水位与已提交前缀和；跨分区乱序不影响终态且可复现，支持并发
  查询与自检。

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
