# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`rename/`](rename/README.md)：可撤销的批量重命名执行器——同批映射同时生效、
  链/环确定性拆分、临时名破环、失败逆序回滚、仅最近成功批次可撤销一次。

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
