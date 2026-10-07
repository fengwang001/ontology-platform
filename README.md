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

## 实例存储与跨类型聚合可见性子系统

位于仓库根包 `ontology`，由三个协作模块构成：

- `store.go`：实例版本仲裁（乐观并发、墓碑版本、按主键点查）
- `aggregate.go`：聚合视图增量维护（`(视图, 分组键)` 汇总单元、原子 apply、全量 rebuild）
- `errors.go` + `kernel.go`：一致性校验、错误归一化与提交/查询编排
- `naive/`：独立的朴素参照模型（全量快照 + 每次查询全表重算），仅供差分对照

关键性质：单把全局提交锁使多视图更新与分组迁移在同一生效时刻整体可见（strict
serializability）；聚合查询不读源实例记录（O(1)，与类型下实例总数无关），
由 `CostMeter` 计数断言 + benchmark 双重证明。

```bash
# 若 go 不在 PATH 或构建缓存目录只读：
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/gocache

# 逐条打印输入/实际输出/判定依据的全量测试（含竞态检测）
go test -race -v ./...

# 查询成本证明（增量 O(1) vs 朴素 O(N)）
go test -run TestQueryCost -v .
go test -run=^$ -bench=BenchmarkQueryCost -benchtime=2000x .
```

设计取舍、被放弃的方案与验证方法见 `docs/DESIGN.md`。
