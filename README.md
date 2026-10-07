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
GOCACHE=/tmp/ontology-go-cache go test ./...

# 带竞态检测与详细输出
GOCACHE=/tmp/ontology-go-cache go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
GOCACHE=/tmp/ontology-go-cache go test -coverprofile=coverage.out ./...
GOCACHE=/tmp/ontology-go-cache go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
GOCACHE=/tmp/ontology-go-cache go vet ./...
```

## 权限时序模块

核心 API、朴素参照实现与设计说明位于：

- `permission/`：提交、排队撤回、历史判定、级联撤销与审计日志
- `docs/temporal-permission-design.md`：语义、取舍、复杂度证明与测试策略
