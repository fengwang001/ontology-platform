# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `scrub/`：多副本块存储的后台校验巡检与修复仲裁服务（副本自洽判定、
  已提交版本推断、权威副本选取、修复写入、到期块选取）。
  设计说明见 `docs/scrub-design.md`。
- `scrub/naive/`：语义相同的独立朴素模型，用于随机操作序列差分对照。

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
