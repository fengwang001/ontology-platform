# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `delegation`：角色委托链模块。角色持有者可将对象类型上属性级与行级权限的子集
  在有效期内委托给其他主体，支持再委托标记、权限收缩级联（全有或全无）、多路径
  独立有效、成环检测、历史判定不可追溯、并发线性化与可观测的遍历开销上界。
  设计说明见 [docs/delegation-design.md](docs/delegation-design.md)。

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
