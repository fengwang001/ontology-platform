# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology`：多租户命名空间权限继承覆盖模块。对象类型定义在全局命名空间并
  被多租户共享，实例归属租户命名空间；权限规则分全局默认与租户覆盖两层，
  跨租户访问按实例归属租户的覆盖规则判定。语义规范、关键取舍与被放弃的
  方案见 [DESIGN.md](DESIGN.md)。
- `cmd/server`：端到端演示程序。

### 快速示例

```go
e := ontology.NewEngine()
e.RegisterTenant("tenant-a")
e.RegisterObjectType(ontology.ObjectTypeDef{
    Name: "contract", Attributes: []string{"title", "amount"},
    RequireRelaxationBasis: true, // 声明：放宽必须附带授权依据
})
e.RegisterInstance(ontology.Instance{ID: "c-1", Type: "contract", OwnerTenant: "tenant-a"})
e.SetGlobalDefault("contract", []ontology.RuleEntry{
    {Action: "read", Attribute: "amount", Effect: ontology.Deny},
})
// 放宽默认拒绝 => 必须带授权依据，否则声明被整体拒绝（missing-basis）
e.SetTenantOverride("tenant-a", "contract", []ontology.RuleEntry{
    {Action: "read", Attribute: "amount", Effect: ontology.Allow, Basis: "dpo-42"},
})
d, _ := e.Decide(ontology.Subject{Tenant: "tenant-b"}, "read", "c-1", "amount")
// d.Effect == Allow；按实例归属租户 tenant-a 的覆盖规则判定
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
