# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 源码映射服务

核心包位于 `sourcemap`，支持登记不可变映射、合成两级映射和按生成位置反查原始位置。

```go
service := sourcemap.NewService()
m1 := sourcemap.Mapping{
    SourceCount: 1,
    Rows: []sourcemap.Row{{
        GeneratedLine: 0,
        Segments: []sourcemap.Segment{
            {GeneratedColumn: 0, Mapped: true, SourceIndex: 0, SourceLine: 3, SourceColumn: 10},
            {GeneratedColumn: 8}, // 显式未映射
        },
    }},
}

if err := service.Register("m1", m1); err != nil {
    log.Fatal(err)
}

result, err := service.Lookup("m1", 0, 2)
if err != nil {
    log.Fatal(err)
}
if result.Mapped {
    fmt.Println(result.SourceIndex, result.SourceLine, result.SourceColumn) // 0 3 12
}
```

合成使用：

```go
err := service.Compose("intermediate-to-source", "final-to-intermediate", "result")
```

结果的源数量继承 `intermediate-to-source`。错误可通过 `sourcemap.CategoryOf(err)` 区分为参数非法、未找到、重复名字和位置溢出。

需要重复查询同一个未登记映射时，先构造一次 `sourcemap.NewIndexedMapping`，之后调用其 `Lookup`；点查为行、段两级二分。详细算法、复杂度证明和取舍见 `DESIGN.md`。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

当前交付为可嵌入的 Go 包，不包含独立网络服务入口。在业务进程中创建 `sourcemap.NewService()` 即可登记、合成和查询映射。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./sourcemap
go test -run TestRandomComposeMatchesNaiveColumnEnumeration ./sourcemap

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

若默认缓存目录只读，可执行：

```bash
PATH=/usr/local/go/bin:$PATH \
GOCACHE=/tmp/go-build-cache \
GOMODCACHE=/tmp/go-mod-cache \
GOPATH=/tmp/go-path \
go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
