# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 行李中转与超重计费

根包实现联程行李直挂判定、提取点切分与计件/计重费用计算，设计取舍见
[DESIGN.md](DESIGN.md)。

- 规则用例与拒绝次序：`go test -v ./...`
- 朴素模型随机差分（打印每步输入/输出/判定依据）：
  `go test -run TestRandomDifferential -v ./...`
- 并发可串行化：`go test -race ./...`
- 性能证据（不随机场数/历史记录数增长）：
  `go test -bench=BenchmarkCheckInScaling -run=^$ .`

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
