# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前交付：本体链接图上的确定性环检测器，详见 [DESIGN.md](DESIGN.md)。

## 环检测器快速上手

```go
g := ontology.NewGraph()
_ = g.AddObjectType("T", "Thing")
_ = g.AddLinkType(ontology.LinkType{
    ID: "to", Direction: ontology.Directed,
    AllowSelfLoop: true, AllowParallel: true,
})
_ = g.AddObject("alice", "a", "T")
_ = g.AddObject("alice", "b", "T")
_ = g.AddLink("alice", "ab", "to", "a", "b")
_ = g.AddLink("alice", "ba", "to", "b", "a")

res, err := g.HasCycle("alice")
// res.HasCycle == true, res.Evidence == ["a", "b", "a"]
```

判定只针对调用者当前权限下的有效子图：先按存在性权限剔除不可见对象及
其全部链接，再按遍历权限剔除链接，最后在剩余子图上检测。结果与规范证
据（长度最短、序列字典序最小的环）在同一图状态下逐字节稳定，与起始对
象、遍历顺序、创建历史无关。

主要 API（`ontology` 包）：

- 类型与图：`AddObjectType`、`AddLinkType`、`AddObject`、`AddLink`、
  `RemoveObject`、`RemoveLink`；
- 权限：`GrantExistence`、`RevokeExistence`、`GrantTraversal`、
  `RevokeTraversal`；
- 检测与观测：`HasCycle`、`SetAuditWriter`、`AuditLog`
  （内部度量通过包内入口 `hasCycleWithStats` 验证，不对调用者暴露）。

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
