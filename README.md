# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`quantile/`](quantile/README.md)：有界质心近似分位数维护器，支持按预算合并相邻质心、
  分位点线性插值、撤回时从真实集合重算、并发只读与精确误差上界审计。

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
