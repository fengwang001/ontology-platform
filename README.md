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

本仓库内核位于 `bcontext/`（详见 `DESIGN.md`）。在受限环境下若 GOCACHE
不可写，可指定临时缓存：

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

# 场景矩阵 + 朴素模型随机差分 + 并发（竞态检测）
go test -race -v ./...

# 复杂度验证：Isolated 与深度无关，FeatureAllowed 随深度线性
go test -bench . -benchtime 1000x -run '^$' ./...

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
