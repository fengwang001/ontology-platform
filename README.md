# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`percolator`](percolator/README.md)：Percolator 式两阶段事务锁解析器
  （主键提交、读者前滚/回滚、保护性 Rollback、确定性可复现），含 2000 组
  随机序列与朴素模型的差分对照测试。

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
