# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子模块

- `cedu/`：执业资格继续教育学分周期核算服务。
  设计说明见 `cedu/DESIGN.md`；包级 API 见 `cedu/doc.go`。

  ```bash
  # 含逐步输入/输出/判定依据的随机对照日志
  go test ./cedu/ -run TestDifferentialRandom -v
  # 竞态检测、复杂度基准
  go test -race ./cedu/
  go test -bench=BenchmarkStatusFixedCost -run=^$ ./cedu/
  ```

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
