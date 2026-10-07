# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `booking/`：短租房源可订日历与预订约束服务（房东封锁、最短入住与换客间隙、
  保留/确认两阶段、取消退款阶梯、一次性改期、日历一致性）。
  设计说明见 `booking/DESIGN.md`，包文档见 `booking/doc.go`。

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
