# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 抑制指令处理器（`suppress` 包）

静态检查工具的抑制指令处理器：输入源文件行数、已知规则集合、诊断与抑制指令，
输出保留/被抑制的诊断（含归属的指令与标签）以及指令自身问题
（缺理由、无目标行、未知规则、重复禁用、孤立启用、未使用、未闭合区间）。

```go
s := suppress.NewSession(totalLines /*1..*/, []string{"A", "B"}, true /* 必须附理由 */)
s.AddDiagnostic(suppress.Diagnostic{Line: 3, Column: 1, Rule: "A"})
s.AddDirective(suppress.Directive{
    Line: 2, Kind: suppress.KindNextLine, Labels: []string{"A"}, Reason: "误报，已建单",
})
j := s.Evaluate() // 线程安全；可与登记并发、可重复调用
```

- 指令种类：`KindThisLine` / `KindNextLine` / `KindDisable` / `KindEnable` / `KindWholeFile`，
  文本形式见 `suppress.ParseKind`（`THIS_LINE` 等）。
- 标签为空等同 `全部`；`全部` 是独立标签，不展开为具体规则，与具体标签互不影响。
- 归属规则：多条同时命中取指令行号最小、并列取登记次序最早、同指令内具体标签先于 `全部`。
- 登记错误：`ErrInvalidParameter`（行号越界、列号 <1、规则名为空、种类未知）优先于
  `ErrDuplicate`（行号/种类/标签集合/理由完全相同）；被拒绝登记不改变会话状态。
- 复杂度：判定为 O((诊断数+标签出现数) log 指令数)，单条诊断命中查询不随指令总数线性增长。

设计取舍、被放弃方案与验证证据见 `suppress/DESIGN.md`。

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
