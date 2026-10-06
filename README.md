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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 门诊药房库存预留与欠药补发

核心实现位于 `pharmacy/`，设计取舍、复杂度证明和验证方法见 `docs/design.md`。

快速验证：

```bash
go test ./...
go test -race ./...
go test -run TestRandomComparisonWithNaive -v ./pharmacy
go test -run '^$' -bench AffectedDrugOnly -benchtime=100x ./pharmacy
```

若当前 shell 找不到 Go，可将命令前缀替换为 `/usr/local/go/bin/go`，并在需要时设置 `GOCACHE=/tmp/go-cache`。
