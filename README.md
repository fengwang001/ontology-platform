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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 本体链接图路径查询

`ontology` 包实现两层声明（对象类型层 / 链接类型层）驱动的遍历准入与最短路径查询。

```go
ps := ontology.NewPermissionState()
ps.UpsertGroup("editors", 1)
ps.AddMember("alice", "editors")
ps.SetObjectDecl("editors", "Document", ontology.Allow) // 对象类型层默认
ps.SetLinkDecl("editors", "comment", ontology.Deny)     // 链接类型层覆盖

g := ontology.NewGraph()
g.AddObject("d1", "Document")
g.AddObject("d2", "Document")
g.AddLink(ontology.Link{Type: "edit", From: "d1", To: "d2", Cost: 2})

eng := ontology.NewQueryEngine(ps, g)
res := eng.ShortestPath(ontology.Query{Subject: "alice", From: "d1", To: "d2"})
// res.Status: reachable | unreachable | ambiguous | invalid-subject | missing-object
// res.Counters: 内部开销度量；res.Steps: 每一步覆盖判定依据
```

- 覆盖判定次序、同优先级拒绝优先、跨层正交优先级冲突的歧义定义见 `DESIGN.md`。
- 查询钉住发起时刻的权限/图快照，查询期间变更不影响本次结果。
- 朴素对照模型 `ontology/naive.go` 与随机差分测试、留痕文件见 `DESIGN.md` 第 6 节。
