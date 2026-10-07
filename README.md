# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 生命周期状态机与校验钩子子系统

- 实现：`ontology/`（状态机执行 `lifecycle.go`、钩子范围注册/解析 `hooks.go`、错误归一化 `errors.go`）
- 设计说明（关键取舍、被放弃方案、复杂度证明、本地验证）：`docs/design.md`
- 使用指南：`docs/usage.md`
- 测试：`tests/`（语义用例、并发/重放、复杂度证明）与独立朴素对照模型 `tests/naive/`

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
