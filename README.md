# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`causalbuf/`](./causalbuf/) —— 基于向量时钟的因果交付缓冲：乱序/重复到达的
  广播消息按因果序交付，暂不能交付的有界缓冲，重复消息丢弃并计数。
  交付条件、级联与重复判定规则详见 [causalbuf/README.md](./causalbuf/README.md)。
  快速体验：`go run ./cmd/vcdemo`（打印输入、判定依据与交付结果）。

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
