# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `bulkimport/`：批量导入子系统。支持分块乱序/重复/并发到达、跨
  块悬挂引用自动重试、失败传播、块数上限超时、提前结束与确定
  性查询。设计说明见 [docs/bulk-import.md](docs/bulk-import.md)。
- `bulkimport/naive/`：独立的朴素参考实现（等全部块到齐再处
  理），用于与增量引擎随机对拍。

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
