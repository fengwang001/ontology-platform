# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `meshauthz/`：服务网格工作负载授权策略评估器。整份策略集原子发布
  （版本单调递增）、拒绝优先、无允许策略默认放行、审计策略仅入依据，
  评估开销与无关命名空间中的策略数量无关。设计取舍见
  [meshauthz/DESIGN.md](meshauthz/DESIGN.md)。

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
