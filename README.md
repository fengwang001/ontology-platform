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

## 变压器容量预约系统

实现在 `transformer/` 包，设计说明见 `docs/DESIGN.md`。

```bash
# 定向边界测试
go test ./transformer/ -v

# 与朴素模型的随机操作序列对拍（逐条打印输入/输出/判定依据）
go test ./transformer/ -run TestDifferentialRandom -v

# 竞态检测
go test -race ./transformer/
```
