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

## 在线考试断线续考/防作弊引擎

本仓库 `package ontology` 同时包含一套在线考试会话引擎（`engine.go`）、
按规则独立重写的朴素参照模型（`naive.go`）与错误分类（`errors.go`）。

- 设计取舍与被放弃方案：见 [DESIGN.md](DESIGN.md)
- 规定边界场景与错误优先级逐对验证：`engine_test.go`
- 与朴素模型的随机对照（400 条轨迹，逐步日志打印输入/输出/判定依据）：`fuzz_test.go`
  - `go test -run TestDifferentialDemoLog -v` 可查看一条完整逐步日志
  - 对照失败时会自动打印整条操作序列
- 并发与 O(1) 判定复杂度证明：`concurrency_test.go`

## 代码检查

```bash
gofmt -l .
go vet ./...
```
