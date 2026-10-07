# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `consteval`：静态语言常量表达式求值器。对调用方构造的结构化常量表达式树，
  按无类型常量的任意精度语义与有类型常量的逐步可表示性检查求出值、种类与
  类型，并支持并发安全地登记命名常量供后续表达式引用。设计说明见
  [DESIGN.md](DESIGN.md)。

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
