# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 泵站水位控制器

- 实现：`pumpstation/`
- 设计说明：`docs/pump-controller.md`
- 边界、并发、随机朴素模型对照：`pumpstation/*_test.go`

```bash
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test ./pumpstation -v
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test ./pumpstation -run '^$' -bench . -benchmem
```

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
