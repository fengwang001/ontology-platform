# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`creditflow`](creditflow/README.md)：基于信用的单发送端 / 单接收端点对点流控
  （信用扣减、积压自动补发、通告与探测、有序消费、可区分错误类别）。

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
