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

## mesh 子包：流量路由配置发布与请求分流

服务网格路由模块位于 `mesh/`，提供有序规则首命中匹配、权重分桶、策略逐字段继承、
发布期遮蔽拒绝与乐观版本原子发布。设计取舍、并发论证与本地验证方法见 `mesh/DESIGN.md`。

```bash
go test -race -v ./mesh
go test -run=NONE -bench=. -benchtime=200000x ./mesh
```
