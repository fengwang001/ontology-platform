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

## 本体路径权限

核心实现位于 `ontology` 包，详细设计见 `docs/design.md`。

```go
graph, err := ontology.NewGraph(objects, links)
policy, err := ontology.NewPolicy(groups, memberships, objectDeclarations, linkDeclarations)
authorizer, err := ontology.NewAuthorizer(policy)

result, err := authorizer.ShortestPath(graph, "user-1", "source-object", "target-object")
if result.Status == ontology.PathReachable {
    // result.ObjectIDs 与 result.LinkIDs 是唯一的最短、字典序优先结果。
}
```

判定顺序为：非法主体 > 链接类型层 > 两端对象类型层 > 默认拒绝。同优先级声明冲突时拒绝优先；声明更新通过原子快照发布，查询固定使用发起时的权限状态。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
