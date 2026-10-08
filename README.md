# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `authz`：服务网格工作负载授权策略评估器。支持命名空间/根命名空间作用域、
  拒绝优先、无允许策略默认放行、空规则与否定条件语义、整份策略集原子版本化发布。
  设计说明见 [authz/DESIGN.md](authz/DESIGN.md)。

```go
store, _ := authz.NewStore("istio-system")
version, err := store.ReplaceAll(policies) // 整体校验，原子生效，版本 +1
result, err := store.Evaluate(authz.Request{ /* ... */ })
// result.Decision / result.DecisionPolicies / result.AuditPolicies / result.Version
```

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
