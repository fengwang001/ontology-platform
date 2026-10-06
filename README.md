# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 级联删除控制器

- 设计说明：[DESIGN.md](DESIGN.md)
- 调用方文档：[API.md](API.md)
- 核心包：根包 `cascade`，入口为 `cascade.NewController()`。
- 删除策略：`Background`、`Foreground`、`Orphan`。
- 所有公开操作同步收敛到稳定状态；并发调用由控制器内部互斥锁串行化。

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

当前容器若默认 Go 缓存目录不可写，可使用：

```bash
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -race -v ./...
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go vet ./...
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -run TestRandomDifferentialAgainstNaiveModel -v ./...
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -run TestOperationCostIsLocal -v ./...
```
