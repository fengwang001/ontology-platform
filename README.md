# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 重复保险理赔分摊子系统

- 设计说明：[`docs/CLAIM_DESIGN.md`](docs/CLAIM_DESIGN.md)
- API 与本地验证：[`docs/CLAIM_USAGE.md`](docs/CLAIM_USAGE.md)
- 生产实现：`internal/claim`；独立朴素参考模型：`internal/naive`

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
