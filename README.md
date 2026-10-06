# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `apf/`：服务端请求的优先级与公平性准入控制器。流分类、按份额划分并发席位、
  流间轮转出队、宽请求队首阻塞、排队超时与配置热更新；时间由调用方注入，
  行为确定可复现。设计说明见 [docs/DESIGN.md](docs/DESIGN.md)。

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
