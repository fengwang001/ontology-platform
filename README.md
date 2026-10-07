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

## 链接实例层仲裁

`ontology/` 包实现两个对象类型之间链接类型的实例层创建/去重仲裁：
双向独立基数（`AtMost(n)`，含 0；或 `Unlimited()`）、区分属性组合去重、
撤销后重占且不继承派生状态、互斥锁保证的可线性化并发、四类可区分失败
（实例不存在 / 方向不允许 / 重复 / 基数已满，实例不存在最优先）、
只依赖当前有效链接的 O(1) 计数，以及输入-依据-结果齐全的审计日志。

关键 API：

```go
s := ontology.NewStore()
s.RegisterObjectType(ontology.NewObjectType("Person", "人"))
s.RegisterObjectType(ontology.NewObjectType("Org", "组织"))
s.RegisterLinkType(ontology.NewLinkType(
    "employs", "Person", "Org",
    ontology.AtMost(1), ontology.Unlimited(), // 正向、反向基数
    []string{"role"},                        // 区分属性
))
s.CreateObject(ctx, "p", "Person")
s.CreateObject(ctx, "o", "Org")

link, err := s.CreateLink(ctx, ontology.CreateLinkInput{
    LinkTypeID:    "employs",
    Direction:     ontology.Forward,
    TailID:        "p",
    HeadID:        "o",
    Discriminator: map[string]string{"role": "dev"},
})
n, _ := s.CountLinks(ctx, "employs", "p", ontology.Forward)
s.DeleteLink(ctx, link.ID())

for _, rec := range s.AuditLog() { // Seq / Input / Basis / Result
    _ = rec
}
```

失败通过 `ontology.AsDecisionError(err)` 后的 `Code()` 区分：
`CodeObjectNotFound`、`CodeLinkTypeNotAllowed`、`CodeDuplicateLink`、
`CodeCardinalityFull`（另有 `CodeLinkTypeNotFound`、`CodeLinkNotFound`）。

设计取舍、被放弃方案、朴素参考模型对拍与复杂度证明见
[`doc/design.md`](doc/design.md)。
