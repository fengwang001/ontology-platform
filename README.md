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

## 分页游标图遍历

核心实现在 `traversal/`，设计说明见 `docs/cursor-traversal.md`。首次请求通过 `Service.Page` 提交 `StartObjectID`、`BatchSize`、`Mode`、`HopLimits` 和 `MaxHops`；后续请求只提交上一页 `NextCursor`、相同模式和批次大小。

```go
page, err := service.Page(traversal.PageRequest{
    StartObjectID: "o00",
    BatchSize:     100,
    Mode:          traversal.ExplicitTruncation,
    HopLimits:     []int{100, 100},
    MaxHops:       2,
})
```

当前环境若 home 目录缓存只读，可使用：

```bash
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
