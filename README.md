# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `costalloc`：逐级下推的内部服务部门成本分摊账本。每个结账期按动态重算的
  比例次序把服务部门成本分摊给尚未下推的部门，余数分按跨期累计分摊额 `H`
  决定归属。规则与公式详见 [costalloc/README.md](costalloc/README.md)。

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
