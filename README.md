# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 聚合视图子系统

跨对象类型、按链接关系分组并对数值属性增量求和的聚合视图：

- `ontology`：视图定义、增量维护引擎（属性写入、链接增删、原子迁移、
  删除级联、乐观并发冲突、原子回滚）与固定优先级错误分类。
- `naive`：独立的朴素全量重算参考模型，用于差分对拍。
- `changelog`：打印每次变更的输入、受影响分组与判定依据。
- 设计与取舍见 `docs/aggregation-views.md`。

```go
store := ontology.NewStore()
engine := ontology.NewEngine(store)
_ = engine.RegisterView(ontology.ViewDef{
    Name: "sales_by_region", GroupType: "region", AggType: "sale",
    LinkType: "sale_in_region", ValueProperty: "amount",
    Contribution: ontology.PolicyFullEach, // 多分组归属贡献方式必须显式声明
})
agg, _ := engine.Query("sales_by_region", "north")
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
