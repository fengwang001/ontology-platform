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

## 占道施工许可审查服务（`occupancy` 包）

见 `occupancy/doc.go` 与 `occupancy/DESIGN.md`。要点：

- 同路段车道数、绕行路线双向、走廊并发上限三类冲突，按固定优先级只报一类。
- 应急抢修仅受同路段车道数约束，可抢占常规许可并按原批准序顺延重审。
- 许可延期（新增部分审查）、撤销（不自动放行历史被挡申请）。
- 任意时刻查询，treap 剪枝保证开销不随历史许可总数增长。
- `NaiveModel` 是独立朴素全量重判模型，随机差分测试与主实现逐操作对照。

```bash
# 随机差分（40 种子 × 1500 操作，打印输入/输出/判定依据）
go test -run TestRandomDifferentialLogged -v ./occupancy
go test -run TestRandomDifferential -v ./occupancy

# 查询复杂度基准（历史 2k/8k/32k，耗时应保持同阶）
go test -bench BenchmarkQueryVsHistory -run '^$' ./occupancy
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
