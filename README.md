# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `heating` — 城市供热管网阀门隔离与停供影响推演服务：管段泄漏时给出
  必须关闭的阀门与失去供热的用户，支持阀门卡死、多处抢修叠加，全部
  操作可并发调用。设计说明见 [docs/heating-design.md](docs/heating-design.md)。
- `heating/naive` — 独立朴素参考模型，用于随机对照测试。

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
