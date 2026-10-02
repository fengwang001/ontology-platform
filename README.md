# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `tcpchk`：RFC 5961 风格 TCP 已建立连接段合法性校验器（序号窗口 /
  RST 精确匹配 / SYN 与 ACK 范围检查 + 全局挑战 ACK 限速），详见
  [tcpchk/README.md](tcpchk/README.md)。

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
