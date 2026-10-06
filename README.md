# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 组件

- `framing/`：反向代理入口的 HTTP/1.x 请求分帧判定器（防请求走私），
  含设计说明 `framing/DESIGN.md`、用法示例 `framing/README.md`，以及对
  独立朴素模型的差分测试、切分等价、并发与零分配验证。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
