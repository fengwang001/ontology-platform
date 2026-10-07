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

## 最大需量控制器（`demand` 包）

工业用户最大需量控制器：按滑动窗口平均功率逼近合同需量，预测越限时按优先级
切除可控负荷，恢复时遵守最短接入/最短断开约束。

- 模块划分与关键取舍：见 [docs/design.md](docs/design.md)
- 核心 API：`NewController` / `Report` / `AddLoad` / `RemoveLoad` /
  `LockLoad` / `UnlockLoad` / `PeakDemand`
- 错误类别（`errors.Is` 判定）：`ErrInvalidParam`、`ErrDataIllegal`、
  `ErrTimeRegression`、`ErrLoadNotFound`、`ErrStateNotAllowed`

```bash
# 全部测试（含与朴素模型对照的随机序列、并发冒烟）
go test ./demand/

# 随机对照日志：打印每步输入、输出与判定依据
go test -v -run TestRandomAgainstNaive ./demand/

# 评估开销与历史长度无关（两档对照）与基准
go test -v -run TestEvalCostIndependentOfHistory ./demand/
go test -bench=BenchmarkReport -benchmem -run=NONE ./demand/
```
