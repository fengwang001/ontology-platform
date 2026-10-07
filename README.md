# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前模块实现：**本体链接图上的权限感知环检测器 `HasCycle`**
（设计说明见 `docs/DESIGN.md`）。

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

# 环检测器相关
go test -run 'TestSelfLoop|TestBidirectionalRoundTrip|TestOnlyCycleEdgeExcludedThenRestored' -v ./ontology
go test -run TestRandomDifferentialAgainstNaive -v ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## HasCycle 快速上手

```go
g := ontology.NewGraph()
_ = g.AddObjectType(ontology.ObjectType{Name: "Thing"})
_ = g.AddLinkType(ontology.LinkType{
    Name: "reports", Direction: ontology.Directed,
    AllowSelf: false, AllowMulti: false,
    SourceType: "Thing", TargetType: "Thing",
})
_ = g.CreateObject(ontology.Object{ID: "alice", Type: "Thing"})
_ = g.CreateObject(ontology.Object{ID: "bob", Type: "Thing"})
_ = g.CreateLink(ontology.Link{ID: "l1", Type: "reports", Source: "alice", Target: "bob"})

// 存在性权限（对象）与遍历权限（链接）两级模型；过滤顺序固定为先对象后链接。
g.SetPermissions("caller-1", ontology.Permissions{
    ExistObject:  map[string]bool{"alice": true, "bob": true},
    TraverseLink: map[string]bool{"l1": true},
})

res, err := g.HasCycle("caller-1")
// res.HasCycle == false；命中自环/双向往返/更长环时 res.Evidence
// 为构成某个环的最小对象集合（按确定性规则规范化）。
```

语义要点：

- 判定次序：调用者标识非法（`ErrInvalidCaller`）> 可见对象为空
  （无环，非错误）> 正常环检测；
- 先剔除无存在性权限的对象（连带其全部链接），再剔除无遍历权限的
  链接，之后才判定环；
- 结果与证据不依赖起始对象或遍历顺序，同一状态多次调用逐元素一致；
- 判定开销只随调用者可见子图规模增长，不随不可见对象/链接增长；
- 并发调用与并发增删、权限变更等价于某个串行顺序。
