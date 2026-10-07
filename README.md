# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 受权限约束的图遍历环检测

`ontology` 包提供“调用方只可见授权标签链接、但环判定基于完整图”的
遍历服务。设计细节、取舍与放弃方案见 `docs/design.md`。

```go
store := ontology.NewGraphStore()
store.AddObject("s"); store.AddObject("a")
_ = store.AddLink(ontology.Link{ID: "l1", From: "s", To: "a", Label: "L1"})

svc := ontology.NewService(store, logger) // logger 可为自定义 ontology.Logger
resp, err := svc.Traverse(ontology.TraverseRequest{
    CallerID: "user-1",
    Start:    "s",
    Labels:   map[string]struct{}{"L1": {}},
    MaxDepth: 8,
})
```

每条 `PathResult` 的判定（`Verdict`）：

- `VerdictNonCyclic`：完全可见且完整图上不成环，返回完整路径。
- `VerdictVisibleCycle`：完全可见，末跳在完整图上闭合真实环。
- `VerdictHiddenCycle`：可见前缀后的不可见延伸闭合真实环，止于边界，
  结果不含任何隐藏链接/对象信息。
- `VerdictPartialInvisible`：可见前缀后有不可见延伸但不成环，成环未知。

错误按固定次序只报第一类：起始对象不存在 → 标签集合为空 → 深度非正
→ 起始对象不可见。遍历绑定开始时刻的单一不可变快照（结构与权限标签
同一时点）。

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
