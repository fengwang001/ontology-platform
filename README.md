# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `underwriting`：人身险投保单核保决策引擎（投保单登记、核保规则库、
  决策裁定与复核）。设计取舍见 [DESIGN.md](DESIGN.md)，用法示例见
  `underwriting/engine_test.go`。

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
