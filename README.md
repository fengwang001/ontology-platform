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

## 死代码裁剪分析器

`deadcode` 包基于模块、声明、导入和导出模型计算必须保留的最小模块/声明集合。

```go
registry := deadcode.NewRegistry()
_ = registry.Register(deadcode.Module{
    ID: "main",
    Declarations: []deadcode.Declaration{
        {Name: "start", References: []string{"helper"}},
    },
    Imports: []deadcode.Import{{
        TargetModule: "lib",
        Bindings: []deadcode.ImportBinding{
            {LocalName: "helper", ImportedName: "helper"},
        },
    }},
    Exports: []deadcode.Export{deadcode.LocalExport("start", "start")},
})

result, err := registry.NewSession([]string{"main"}).Solve()
```

结果包含按标识排序的 `IncludedModules`、`KeptDeclarations`，以及每个声明的保留原因（`entry-export`、`referenced`、`side-effect`）。无法解析的导入会返回可类型断言为 `*deadcode.AnalysisError` 的错误，并通过 `Code` 区分缺失、歧义和重导出循环。

完整设计、复杂度取舍、放弃方案与本地验证说明见 `docs/dead-code-elimination-design.md`。
