# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 图遍历服务（`ontology` 包）

支持自环与平行链接的沿链接类型方向集合遍历，严格区分多路径重复到达与真实环路，
返回每条路径的终态（`boundary` / `cycle` / `depth-limit`）。环路判定严格基于
当前路径祖先序列（O(1) 哈希探测），不使用全局 visited 集合；遍历基于
copy-on-write 不可变快照，与并发增删改互不阻塞。详见 `ontology/DESIGN.md`。

```go
g := ontology.NewMemGraph()
g.AddObject("a"); g.AddObject("b")
g.DefineLinkType("knows")
g.AddLink(ontology.Link{ID: "l1", Type: "knows", Source: "a", Target: "b"})

logger := ontology.NewMemoryLogger()
svc := ontology.NewTraverseService(g, logger)
res, err := svc.Traverse(ontology.TraverseRequest{
    Start:     "a",
    LinkTypes: map[ontology.LinkType]ontology.Direction{"knows": ontology.DirectionOut},
    MaxDepth:  5,
})
// res.Paths[i].Reason: TerminatedBoundary / TerminatedCycle / TerminatedDepthLimit
// res.AncestorChecks: 环路祖先核对总次数（= 展开的候选边数）
fmt.Println(logger.Render()) // 输入 + 每条路径终态 + 判定时祖先序列
```

校验错误按固定互斥次序返回：起始对象不存在 → 方向集合为空/含未定义类型/方向非法 →
深度上限非正整数；被拒绝的请求不产生任何部分结果。同一跳同时满足环与深度上限时，
固定报告为环（自环在首跳即被判定）。

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
