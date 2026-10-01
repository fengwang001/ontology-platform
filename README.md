# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`regbank/`](./regbank/README.md)：带访问类型（RW/RO/WO/W1C/W1S/RC）的 32 位寄存器位域模拟器，支持字节使能写、软件读改写（RMW）、硬件侧置位、复位与无副作用 `Raw`，并发安全。语义、字节使能规则、RMW 展开与验证方法见 `regbank/README.md`。

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
