# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `meeting`：在线会议室发言权控制服务（举手排队、发言权授予与限时收回、
  强制静音、主持人移交；惰性到期、并发可串行化、结果可重放）。
  设计说明见 [docs/meeting-room-design.md](docs/meeting-room-design.md)。
- `meeting/naive`：会议室服务的独立朴素参考实现，仅用于差分对照测试。

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
