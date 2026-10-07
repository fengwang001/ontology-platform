# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 本体链接图三态可达性判定器

`ontology` 包实现链接图上的可达性判定，严格区分对象的**存在性权限**与链接的
**遍历权限**，返回三个互斥结果：

- `Reachable`：存在调用者可见且可遍历的路径；
- `Unreachable`：两端可见，且忽略一切权限后图中也不存在该路径（穷尽证明）；
- `RestrictedUnknown`：端点不可见，或候选路径被遍历/存在性权限截断而无法证否。

判定次序为：标识非法 → 对象事实缺失 → 端点对调用者不可见 → 权限感知搜索（认证
BFS，未命中再以地面真值 BFS 区分「截断」与「穷尽」）。查询在入口获取一次不可变
快照，授予/撤销与查询的并发历史整体等价于某个串行顺序。内部度量只统计本次查询
实际展开的对象/链接，追加调用者无权触达的区域时代价不增长。

详细设计、取舍、被放弃方案与验证方法见 [DESIGN.md](DESIGN.md)。

最小用例：

```go
g := ontology.NewGraph()
_ = g.AddObjectType("Person")
_ = g.AddLinkType("knows", ontology.Unidirectional)
_ = g.AddObject(ontology.Object{ID: "alice", ObjectType: "Person"})
_ = g.AddObject(ontology.Object{ID: "bob", ObjectType: "Person"})
_ = g.AddLink(ontology.Link{ID: "l1", LinkType: "knows", Src: "alice", Dst: "bob"})
g.GrantExistence("u", "alice")
g.GrantExistence("u", "bob")
// 未授予 traversal 前：RestrictedUnknown（候选路径被截断）
out, _ := g.ReachableFrom(context.Background(), "alice", "bob", "u")
g.GrantTraversal("u", "knows")
out, _ = g.ReachableFrom(context.Background(), "alice", "bob", "u") // Reachable
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
