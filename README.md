# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `importer/`：批量导入子系统。支持乱序/重复到达的数据块、跨块悬挂
  引用的自动落地与失败传播、块数上限超时、提前结束、并发处理；
  `importer/naive/` 为独立朴素参照模型，用于随机对拍。
  设计说明见 `docs/batch-import-design.md`。

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
