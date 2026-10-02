# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `iblt`：可逆布隆查找表（IBLT）集合对账草图，把两个副本的键集合各自
  写入草图、相减后剥离出各自独有的键。位置与校验的计算、纯格子与剥离
  次序、错误优先级及本地验证方法见 [iblt/README.md](iblt/README.md)。

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
