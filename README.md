# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多粒度意向锁层级管理器

`ontology.LockManager` 支持资源树上 IS/IX/S/SIX/X 五种锁模式的授予、
按 join 转换与释放，保证祖先意向齐全与同节点模式相容，并发可线性化。

- 设计说明（强度偏序、join、相容矩阵、转换规则、祖先意向、错误与验证方法）：
  `docs/lock_manager.md`
- 对拍测试使用 2000 组随机操作序列与逐步朴素参考模型逐操作比对，
  可通过 `go test -run TestRandomDifferential -difflog -v` 打印
  输入、输出与每步判定依据。

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
