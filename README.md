# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`dmaring`](dmaring/README.md)：DMA 描述符环主机侧驱动模型。无界指针
  `prod/dev/reap` + 槽号取余，按 OWN 位交接分散聚集包，支持设备出错连带
  丢弃与主机整包回收；规则、错误码与本地验证方法见该目录文档。

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
