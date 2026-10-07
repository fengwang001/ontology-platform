# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：临床检验标本采集、送检与签收拒收

- 实现：`lab/`（`Clock` 时钟、`Catalog` 目录快照、`Patient`/`AppItem`
  状态机、`Tube` 标本管、`System` 串行化门面、`Metrics` 复杂度计数）。
- 设计说明（关键取舍、被放弃的方案、本地验证方法）：`DESIGN.md`。
- 演示程序：`cmd/labdemo`（端到端场景 + 两档规模复杂度对照）。

## 环境要求

- Go 1.26+（`go version` 确认）
- 若 Go 缓存目录不可写：`export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath`

## 运行

```bash
# 运行演示（场景流程 + 两档规模复杂度对照）
go run ./cmd/labdemo
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 随机序列与独立朴素模型对照（1500 组，逐步日志见测试输出路径）
go test ./lab/ -run TestRandomizedAgainstNaiveModel -v

# 签收/查询复杂度的两档规模对照
go test ./lab/ -run TestSignAndQueryComplexityTwoScales -v
go test ./lab/ -bench .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
