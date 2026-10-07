# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `incremental`：增量导出的位点推进与去重合并组件。负责导出周期的
  位点声明与确认、周期内/跨周期去重、位点记录不可读时的安全推导，
  以及多链路并发导出。设计说明见 [docs/incremental-export.md](docs/incremental-export.md)。

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
