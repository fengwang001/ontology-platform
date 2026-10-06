# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `logkv`：只追加日志结构键值引擎（启动恢复、撕裂尾部截断、提示信息、
  读取自愈、段合并与崩溃清理）。见 [logkv/README.md](logkv/README.md)
  与 [logkv/DESIGN.md](logkv/DESIGN.md)。

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
