# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 团队看板服务

核心实现位于 `kanban` 包。入口为 `kanban.NewService`，支持看板创建、卡片创建、乐观版本移动、重开、改负责人、依赖增删、列上限调整和快照查询。

```go
service := kanban.NewServiceWithLogger(logger)
err := service.CreateBoard("release", kanban.BoardConfig{
    Columns: []kanban.Column{
        {Name: "todo"},
        {Name: "dev", Limit: 3},
        {Name: "review", Limit: 2},
        {Name: "done"},
    },
    AssigneeLimit: 5,
})
```

非完成列 `Limit=0` 表示不限；完成列不设上限。所有错误均为包内 `kanban.Err...` 常量，可用 `errors.Is` 判断。详细取舍和验证方式见 `DESIGN.md`。

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
