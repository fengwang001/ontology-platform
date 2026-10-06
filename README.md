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

## 包说明

- `ecscache`：支持客户端子网（ECS）扩展的递归解析缓存。按应答声明的
  适用范围分别缓存多份结果，按客户端地址选择适用条目，并合并同源前
  缀的并发上游查询；时钟与上游解析器由调用方注入。设计说明见
  [docs/ecscache-design.md](docs/ecscache-design.md)。
