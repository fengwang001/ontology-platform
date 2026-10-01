# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`rename/`](./rename/README.md)：版本管理重命名检测器。登记被删除/新增文件，
  `Detect` 按「精确配对 → 相似度配对」两阶段与固定排序键产出与登记顺序无关的
  重命名结果，支持并发、结果记忆化与文件大小剪枝。

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
