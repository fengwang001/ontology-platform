# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

- **受权限约束的图遍历环检测**（`ontology` 包）：
  - 每条链接附带权限标签，调用方仅可见标签集合范围内的链接；
  - 环路检测基于完整图（含不可见链接）判定，结果对所有调用方一致；
  - 仅由不可见链接闭合的环路返回 `CYCLE(hidden)` 标记，不泄露链接属性；
  - 不可见但不成环的延伸返回 `HIDDEN-EXT`（部分不可见）标记；
  - 图结构与权限标签共享同一快照时点，并发修改不影响本次遍历；
  - 祖先核对每跳 O(1)，次数不随图总规模增长（`Stats` 可验证）。
- 设计取舍与被放弃的方案见 [docs/design.md](docs/design.md)。

## 快速示例

```go
store := ontology.NewStore()
_ = store.AddLink(ontology.Link{ID: "l1", From: "A", To: "B", Label: "public"})
_ = store.AddLink(ontology.Link{ID: "l2", From: "B", To: "A", Label: "secret"})

res, err := ontology.Traverse(store, ontology.Request{
    CallerLabels: []ontology.Label{"public"},
    Start:        "A",
    MaxDepth:     10,
}, nil)
// A -l1-> B，B 处得到 CYCLE(hidden)：环路在完整图上成立，
// 但闭合链接 l2 的任何属性都不会泄露。
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
