# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模块

- `ontology`：家庭医疗保单多层限额账引擎（项目/个人年度/家庭年度/个人终身四层限额，
  支持理赔扣减、冲正、限额批改与多保单并发）。设计取舍见 [DESIGN.md](DESIGN.md)。

### 快速示例

```go
e := ontology.NewEngine()
_ = e.RegisterPolicy(ontology.PolicyInput{
    ID: "P1", StartDay: 0, YearLength: 365, FamilyAnnualLimit: 50000,
    Members: []ontology.MemberInput{{ID: "m1", AnnualLimit: 20000, LifetimeLimit: 100000}},
    Items:   []ontology.ItemInput{{ID: "i1", AnnualLimit: 5000}},
})
pay, err := e.SettleClaim("P1", ontology.ClaimInput{
    ID: "c1", MemberID: "m1",
    Lines: []ontology.LineInput{{ItemID: "i1", Day: 100, Amount: 6000}},
})
// pay == [5000]（受项目年度限额约束）
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
