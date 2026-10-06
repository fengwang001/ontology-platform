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

## 公交线路排班与串车调整（`bus` 包）

- 设计说明：`bus/DESIGN.md`（关键取舍、被放弃方案、验证方法）。
- 模块：`scheme.go` / `errors.go` / `service.go` / `intervention.go` / `naive.go` / `doc.go`，
  测试：`service_test.go`（8 类边界）与 `diff_test.go`（40 组随机序列对照朴素模型、
  并发串行等价、确定性重放、O(1) 查询基准）。
- 验证：

```bash
go test -race -count=3 ./bus/
go test -bench=BenchmarkGetEvent -run=^$ ./bus/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
