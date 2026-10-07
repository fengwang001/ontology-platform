# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前已交付 `ontology` 包：对象类型字段演进的兼容性校验机制——判断一次
字段定义变更能否在不迁移既有实例数据的前提下被新版本读写路径安全接受。
完整设计、关键取舍与被放弃方案见 [DESIGN.md](DESIGN.md)。

能力概览：

- 六类互斥的变更分类（新增带/不带默认值、收紧、放宽、改类型、删除），
  另有复合约束与无实际变化两个哨兵类别；
- 收紧全量存活实例校验、放宽免扫描、类型变更无损精确重解释；
  新增必填字段需允许缺失或提供覆盖全部存活实例的回填规则；
- 链接类型/动作引用的语义漂移检查；多类不兼容一次性全部暴露；
- 打包提交 all-or-nothing、零状态变化拒绝；
  `Submit`/`SubmitCAS` 与实例写入在同一临界区下可串行化；
- 扫描量以当前存活实例数为上界，与历史删除/迁移总量无关；
- 每次判定写入审计记录（变更、检查依据、结论）。

最小用法：

```go
store := ontology.NewInstanceStore()
refs := ontology.NewReferenceRegistry()
log := ontology.NewMemoryAuditLog()
ot := ontology.NewObjectType("Order", store, refs, log)

report, err := ot.Submit([]ontology.FieldChange{{
    Name: "priority",
    New: &ontology.FieldDef{
        Type:       ontology.NewIntType(),
        Constraint: ontology.NewRangeConstraint(0, 10, true, true),
        // 不带默认值时：AllowMissing=true，或提供 BackfillRule
        AllowMissing: true,
    },
}})
if err != nil { // *ontology.RejectError，report.Reasons 为全部不兼容类别
    fmt.Println(report)
}

version, err := ot.WriteInstance("o1", map[string]ontology.Value{
    "priority": ontology.NewValue(int64(3)),
}) // version 即该写入锁定的字段定义版本
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
