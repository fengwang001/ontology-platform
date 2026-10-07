# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前模块 `ontology/` 实现本体链接图上的**三态可达性判定器**：区分对象存在性权限与链接遍历权限，返回可达 / 不可达 / 受限未知三个互斥结果。设计取舍、并发语义、度量定义与验证方法见 [`docs/DESIGN.md`](docs/DESIGN.md)。

## 三态可达性

- 包入口：`ontology.NewGraph()` 与 `(*Graph).Reachable(from, to, caller)`。
- 判定次序：非法标识（`ErrInvalidID`）> 图中确实不存在（`ErrNotFound`）> 端点对调用者不可见（`Restricted`）> 三态图搜索。
- 结果：`ontology.Reachable`、`ontology.Unreachable`、`ontology.Restricted`；判定依据（分类理由、被截断候选弧、内部度量）见返回的 `*ontology.Trace`。
- 朴素穷举参照实现位于 `ontology/naive`，仅供随机差分测试使用；查询对照日志输出到 `ontology/testdata/differential_queries.jsonl`。

```go
g := ontology.NewGraph()
// 注册类型 / 实例 / 链接，授予存在性与遍历权限 …
out, trace, err := g.Reachable("order-1", "customer-7", "alice")
// out ∈ {Reachable, Unreachable, Restricted}
// trace.Reason 记录三态分类依据；trace.Metrics 是内部成本度量
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
