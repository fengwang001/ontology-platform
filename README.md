# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`replay/`](replay/README.md)：整数令牌桶 + 逻辑时钟的限速回放器，支持基准/追赶两种
  补充速率、桶容量突发拆平、FIFO 保序与失败整体回滚，并提供并发安全的快照与自检。

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
