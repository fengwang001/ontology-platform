# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 子系统

- `defassign/`：编译器前端的确定赋值检查子系统。对带顺序、条件分支、
  循环、提前跳出与异常保护结构（被保护体/处理分支/清理区域）的程序，
  判定每处读取是否在所有到达路径上都已赋值，并诊断「可能未赋值」的读取
  （附精确到达路径）与「从未被读取」的赋值。设计说明见
  `defassign/DESIGN.md`，用法示例见 `defassign/flow_test.go`。
