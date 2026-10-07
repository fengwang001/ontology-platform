# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子类型判定包 `subtype`

`ontology/subtype` 提供支持递归命名类型的结构化子类型判定：

- 类型表达式：整数、浮点、字符串、布尔、顶类型、底类型、对象、函数、
  联合、命名类型引用。
- 登记命名类型定义（支持自引用、相互引用与前向引用）后，可判定任意两个
  类型表达式的子类型关系，取满足全部规则的最大解（共归纳语义）。
- 非法参数、重复定义、未定义引用、无保护循环以可区分的错误类别报告。
- 并发安全：登记与判定等价于按某个串行顺序执行，判定基于一致快照。

```go
r := subtype.NewRegistry()
_ = r.Register("A", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "A"})))
_ = r.Register("B", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "B"})))

ok, err := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"}) // true
ok, err = r.Check(subtype.Int{}, subtype.Float{})                  // true
```

设计取舍、被放弃的方案与验证方法见 [subtype/DESIGN.md](subtype/DESIGN.md)。

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
