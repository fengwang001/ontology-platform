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

## 模块：动态标签属性级权限（`dlabel`）

敏感标签不登记、不缓存，由实例当前属性取值按声明规则实时计算；
基于多版本并发控制提供可重复读，标签冲突按 deny-overrides 唯一合并。

- 设计说明（取舍、放弃方案、证明手段、本地验证）：`dlabel/DESIGN.md`
- 快速上手：

```go
p := dlabel.NewPlatform([]string{"root"})
p.RegisterObjectType("root", "Person", map[string]dlabel.ValueKind{"age": dlabel.KindInt})
p.SetRule("root", dlabel.Rule{ObjectType: "Person", Tag: "minor",
    Body: dlabel.AttrAtom("age", dlabel.OpLt, dlabel.IntValue(18))})
p.SetGrant("root", dlabel.Grant{Subject: "alice", Tag: "minor",
    Read: dlabel.EffectDeny, Visibility: dlabel.EffectAllow, Attrs: []string{"*"}})
p.CreateInstance("root", "Person", "p1", map[string]dlabel.Value{"age": dlabel.IntValue(10)})

snap := p.Begin("alice")              // 固定快照，可重复读
res, _ := snap.ReadAttributes("alice", "Person", "p1", []string{"age"})
res.Attrs["age"].Allowed              // false：minor 标签 deny 生效
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
