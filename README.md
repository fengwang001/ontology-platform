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

## subtype 包

`subtype` 实现支持递归命名类型的结构化子类型判定：

```go
reg := subtype.NewRegistry()
_ = reg.Register("List", subtype.Obj(
    subtype.Prop{Name: "head", Type: subtype.Int()},
    subtype.Prop{Name: "tail", Type: subtype.Ref("List"), Optional: true},
))
ok, err := reg.IsSubtype(subtype.Ref("List"), subtype.Top())
```

支持基本类型、顶/底类型、对象（可选/只读属性）、函数（参数逆变）、
联合与命名引用；递归取最大解（共归纳语义）；登记与判定并发安全；
错误区分参数非法、重复定义、未定义引用与无保护循环。
设计取舍与验证方法见 [DESIGN.md](DESIGN.md)。
