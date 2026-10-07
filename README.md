# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `sw/`：服务 Worker 注册与版本更新协调器（注册作用域匹配、版本生命周期、客户端控制、
  更新检查与缓存清单）。见 [sw/README.md](sw/README.md) 与 [sw/DESIGN.md](sw/DESIGN.md)。

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
