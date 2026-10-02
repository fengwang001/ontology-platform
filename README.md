# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `dbscan`：滑动窗口增量 DBSCAN 聚类服务。在二维整数点随插入、删除与
  时间过期持续变化时，维护每个点的核心 / 边界 / 噪声身份与确定的簇
  标签，精确报告每次操作的标签变化（Changes）与簇事件（Events），
  支持并发调用。定义、规则与验证方法见 [dbscan/README.md](dbscan/README.md)。

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
