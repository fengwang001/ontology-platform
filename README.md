# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`constexpr`](constexpr/DESIGN.md)：静态语言常量表达式求值器。无类型常量按
  任意精度（`big.Int`/`big.Rat`）语义求值，有类型常量逐步做可表示性检查；
  支持并发安全的命名常量登记与 O(1) 引用。设计、取舍与验证方法见
  [`constexpr/DESIGN.md`](constexpr/DESIGN.md)。

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
