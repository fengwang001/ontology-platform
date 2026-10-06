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

## 抑制指令处理器

核心实现位于 `suppress` 包，提供线程安全的诊断与指令登记、可重复判定和一致快照：

```go
processor := suppress.NewProcessor(totalLines, []string{"A", "B"}, true)
if err := processor.RegisterDiagnostic(suppress.Diagnostic{Line: 3, Column: 2, Rule: "A"}); err != nil {
	return err
}
if err := processor.RegisterDirective(suppress.Directive{
	Line:   3,
	Kind:   suppress.KindLine,
	Tags:   []string{suppress.AllRules},
	Reason: "false positive after triage",
}); err != nil {
	return err
}
report := processor.Decide()
```

指令种类：

- `KindLine`：作用于所在行。
- `KindNextLine`：作用于下一行；末行报“无目标行”。
- `KindDisable` / `KindEnable`：按标签独立配对的半开区间。
- `KindFile`：对整个文件生效，不受所在位置影响。

判定结果包含保留诊断、被抑制诊断及其归属 `(指令行号, 标签)`，以及标签级和指令级问题。设计、复杂度证明和放弃方案见 `suppress/DESIGN.md`。

专项验证：

```bash
GOCACHE=/tmp/go-build-ontology go test ./suppress -v
GOCACHE=/tmp/go-build-ontology go test -race ./suppress
GOCACHE=/tmp/go-build-ontology go test -bench BenchmarkDecide ./suppress
```
