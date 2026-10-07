# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包结构

- `graph`：对象-链接图存储，线程安全，支持遍历开始时的一致性快照（`Store.Snapshot`）。
- `traverse`：支持**深度上限 + 结果条数上限**双维度独立限制的图遍历服务，
  四种两两互斥的截断标记（`depth_only` / `limit_only` / `depth_then_limit` /
  `limit_then_depth`），确定且可复现的分支展开次序，快照隔离，结构化日志。
  语义、取舍与验证方法详见 [DESIGN.md](DESIGN.md)。
- `cmd/server`：演示程序，一次遍历展示四种截断标记。

## 快速开始

```go
store := graph.NewStore()
_ = store.AddObject(graph.Object{ID: "a", Type: "Thing"})
_ = store.AddObject(graph.Object{ID: "b", Type: "Thing"})
_ = store.AddLink(graph.Link{ID: "l1", Type: "owns", SourceID: "a", TargetID: "b"})

svc := traverse.NewService(store, nil) // nil 使用默认 slog 日志器
res, err := svc.Traverse(traverse.Request{
    StartID:     "a",
    Directions:  []graph.Direction{graph.Outgoing},
    DepthLimit:  3,   // 任意路径最多扩展 3 跳
    ResultLimit: 10,  // 整次遍历最多返回 10 条路径
})
// res.Returned：已返回路径及其标记；res.Truncated：被截断分支及其标记；
// res.Stats.BookkeepingOps：条数上限判定的簿记操作次数（与上限数值无关）。
```

错误按固定次序只报第一类：`ErrStartObjectNotFound` → `ErrInvalidDepthLimit`
→ `ErrInvalidResultLimit` → `ErrEmptyDirections`，被拒绝的请求不产生部分结果。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行演示
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
go test ./traverse
go test -run TestAllFourMarkersInOneTraversal ./traverse

# 簿记开销与条数上限数值无关的基准证据
go test -run=NONE -bench=BenchmarkTraverseLimitMagnitude ./traverse/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
