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

## 接触者追踪子系统

- 设计与取舍：[docs/DESIGN.md](docs/DESIGN.md)
- API 使用：[docs/API.md](docs/API.md)
- 核心包：`contacttracing/`；独立朴素模型：`naivemodel/`；随机差分测试：`contacttracing/difftest/`
- 运行：`go test ./...`（本机 Go 位于 `/usr/local/go/bin`，如缓存目录只读可 `export GOCACHE=/tmp/gocache`）
