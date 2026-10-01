# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `bencode`：bencode 流式增量解码器。任意切分的字节流还原为值树，
  规范性破坏时按首个违规字节精确拒绝，结果与切分方式无关。
  规范性规则、错误偏移约定与本地验证方法见 [bencode/README.md](bencode/README.md)。

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
