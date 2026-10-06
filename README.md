# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 渲染树布局失效与重排内核（`layout` 包）

由盒树维护、脏标记传播、布局边界判定、重排执行与度量查询五个协作模块组成，
支持固定值/内容决定两种宽高模式、内边距、布局隔离、插入/移除/移动与最小子树重排。

- 设计与取舍：`layout/DESIGN.md`
- 独立全量重算朴素模型（测试 oracle）：`layout/naive.go`
- 测试：`go test -race -v ./layout`

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
