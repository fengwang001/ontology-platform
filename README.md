# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 证书选择器

服务器名称证书选择器位于 `certselector` 包，支持证书热更新、精确/通配 SAN 匹配、默认证书回退和按密钥类型、失效时刻、证书 ID 的确定性排序。

```go
selector := certselector.New()
err := selector.Add(certselector.Certificate{
    ID:        "edge-ec",
    Names:     []string{"*.example.com"},
    KeyType:   certselector.KeyTypeEC,
    NotBefore: 1,
    NotAfter:  3600,
})

selection, err := selector.Select(certselector.SelectInput{
    Name:     "api.example.com",
    KeyTypes: map[certselector.KeyType]bool{certselector.KeyTypeEC: true},
    Now:      60,
})
```

选择来源由 `Selection.Source` 区分：`MatchExact`、`MatchWildcard`、`MatchDefault`。完整规则、复杂度和方案取舍见 `docs/certselector-design.md`。

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
