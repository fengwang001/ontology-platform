# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 图遍历服务

`graph` 包提供带独立深度上限和结果条数上限的确定性路径遍历。

```go
store := graph.NewGraph()
store.AddLink(graph.Link{FromID: "a", ToID: "b", Type: "knows"})

snapshot := store.Snapshot()
result, err := snapshot.Traverse(graph.TraverseRequest{
    StartID:     "a",
    MaxDepth:    3,
    ResultLimit: 100,
    Directions:  []graph.Direction{graph.Outgoing},
})
```

每条未完整展开的分支位于 `result.Truncated`，原因是 `DepthOnly`、`LimitOnly`、`DepthBeforeLimit` 或 `LimitBeforeDepth` 之一。传入 `Logger` 可记录本次上限、每条路径、截断前缀和归类依据。

设计取舍、快照一致性和测试策略见 `docs/design.md`。

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
