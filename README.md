# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology`：权限沿链接类型图传播与覆盖的判定网关。支持有向
  链接类型的深度受限传播、目标类型的两种覆盖形态（替换 /
  替换并阻断）、传播环路检测、显式授权（含否定项）优先、
  线性一致的并发语义与规模无关的判定性能。设计取舍见
  [docs/design.md](docs/design.md)。
- `cmd/server`：演示程序，构建示例图并打印判定结果与依据。

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
