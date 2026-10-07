# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `traversal/`：图遍历服务。在允许自环与平行链接的图上，严格区分
  「多路径重复到达」（菱形汇聚，照常扩展）与「真实环路」（目标位于
  当前路径自己的祖先序列，立即终止并标记）。终态分为 `boundary` /
  `cycle` / `depth_limit`；同跳既是环又触达深度上限时固定报环。
  遍历基于开始时刻的不可变快照执行，遍历期间的并发链接增删不会混入。
  设计取舍、被放弃方案与验证方法见 [`DESIGN.md`](./DESIGN.md)。

最小用法：

```go
g := traversal.NewGraph()
_, _ = g.Batch(traversal.Mutation{
    AddLinkTypes: []traversal.LinkTypeID{"knows"},
    AddObjects:   []traversal.ObjectID{"a", "b"},
    AddLinks: []traversal.Link{
        {ID: "e1", Type: "knows", Source: "a", Target: "b"},
    },
})
svc := traversal.NewService(g, nil)
res, err := svc.Traverse(ctx, traversal.TraversalRequest{
    Start:      "a",
    Directions: map[traversal.LinkTypeID]traversal.Direction{"knows": traversal.DirOutbound},
    MaxDepth:   5,
})
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
