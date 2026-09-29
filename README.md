# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`dimcache`](dimcache/README.md)：由变更数据捕获（CDC）驱动的维表
  查询缓存失效机制——版本栅栏、两阶段读取回填、负缓存、乱序/延迟事件
  处理，以及与无缓存直读源头的最终一致性对照。

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
