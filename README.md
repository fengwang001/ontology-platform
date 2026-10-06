# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 重载决议子系统

`package overload` 提供确定性的静态重载决议：

- 使用 `NewRegistry` 登记类型、提升转换、用户定义转换和同名函数声明。
- 使用 `Registry.Resolve` 对一次 `Call` 返回完整 `Report`。
- 报告包含选中声明、歧义候选、无匹配原因、逐位置转换等级、默认参数和可变参数使用情况。
- 转换等级依次为恒等、提升、用户定义；一次转换最多一个用户定义转换，其前后各允许一次提升。
- 平局规则固定为：非可变候选、使用默认参数更少、形参类型更特化。
- 并发决议读取不可变快照；登记通过互斥锁整体替换，决议不会看到半更新状态。

最小示例：

```go
r := overload.NewRegistry(overload.WithLogWriter(io.Discard))
_ = r.DefineType("int")
_ = r.AddPromotion("int", "any")
_ = r.AddDeclaration(overload.Declaration{
    Name:   "print",
    Params: []overload.Param{{Type: "any"}},
})
report := r.Resolve("print", overload.Call{Args: []overload.TypeID{"int"}})
```

详细取舍见 `DESIGN.md`。

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
