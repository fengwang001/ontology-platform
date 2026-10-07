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

## 权限固化导出与审计追溯（`ontology` 包）

对象快照导出会把导出时刻“执行主体对各属性的权限判断”固化进结果：
被排除属性附带不可变的命中规则版本标识；之后规则删除、覆盖或继承链重组
都不影响历史导出，审计 `Trace` 仍能解析出命中时刻的规则内容快照。

```go
p := ontology.New()
p.CreateType(ctx, "Employee", nil)
p.CreateType(ctx, "Manager", []string{"Employee"})
p.CreateSubject(ctx, "auditor")
p.PutRule(ctx, "Employee", "auditor", "salary", ontology.EffectDeny)

rec, err := p.Export(ctx, ontology.ExportRequest{
    ExportID: "report-1", TypeID: "Manager", ObjectID: "mgr-42",
    SubjectID: "auditor",
    Attributes: []string{"name", "salary"},
})
// rec.Excluded[0] 固化 {Attribute:salary, VersionID, RuleID, DeclaringType:Employee}

// ……之后删除规则 / 重组继承链……
res, err := p.Trace(ctx, "report-1", "salary")
// res.Rule 是命中时刻的不可变内容快照；类型为 *RuleVersion
```

错误类别（`ontology.KindOf(err)`）：

- `OBJECT_TYPE_NOT_FOUND`（导出时优先级最高）
- `SUBJECT_NOT_FOUND`
- `EXPORT_NOT_FOUND`（审计时优先级最高）
- `ATTRIBUTE_NOT_EXCLUDED`（该属性当时未被排除，非记录缺失）

判定日志使用 `p.WithLogger(ontology.NewJSONLogger(os.Stdout))`，
逐属性打印输入、INCLUDE/EXCLUDE 输出与命中依据。

设计取舍、被放弃方案、并发串行化与 O(1) 解析证明见 `docs/design.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
