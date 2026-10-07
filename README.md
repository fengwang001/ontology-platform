# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：对象生命周期状态机与校验钩子

- `ontology/` — 核心实现：生命周期状态机定义与转移执行（`lifecycle.go`）、
  按阶段注册与 O(命中数) 解析钩子触发范围（`hooks.go`）、错误归一化（`errors.go`）。
- `ontology/naive/` — 朴素参考模型（全量遍历），仅用于差分测试对照。
- `docs/design.md` — 设计说明：关键取舍、被放弃的方案、本地验证方法。

```go
ot, _ := ontology.NewObjectType(ontology.ObjectTypeDef{
    Name:      "doc",
    Stages:    []ontology.Stage{"draft", "review", "archived"},
    Edges:     []ontology.Edge{{"draft", "review"}, {"review", "draft"}, {"review", "archived"}},
    Terminals: []ontology.Stage{"archived"},
})
ot.Hooks().RegisterTransition(&ontology.Hook{Name: "t", From: "draft", To: "review", Fn: myCheck})
ot.Hooks().RegisterEnter(&ontology.Hook{Name: "e", To: "review", Fn: myCheck})
inst, _ := ot.NewInstance("obj-1", "draft")
err := ot.Transition(inst, "review") // 具体转移钩子先于进入钩子触发
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
