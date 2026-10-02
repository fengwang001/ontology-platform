# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `rbac`：带层级角色与 SSD/DSD 职责分离约束的 RBAC 管理器（角色继承、
  用户分配、会话激活、权限检查、级联停用）。详见 [docs/rbac.md](docs/rbac.md)。

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
