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

## 历史时刻一致快照遍历子系统（`temporal`）

`temporal/` 实现了针对指定历史时刻对对象/链接图的一次性一致遍历：
遍历钉住一个逻辑时刻，链接存在性、对象属性、对象类型属性定义与
链接基数版本全部锚定该时刻；遍历进行期间的并发写入、属性定义迁移与
基数调整不会混入或改变任何一步的判定。

- 设计与取舍（含被放弃方案、复杂度证明与本地验证方法）：
  [`docs/DESIGN.md`](docs/DESIGN.md)
- 包级 API 文档：[`temporal/doc.go`](temporal/doc.go)
- 端到端示例：`go run ./cmd/demo`

关键测试：

```bash
# 迁移/基数边界穷举
go test ./temporal -run 'TestPropertyMigration|TestCardinality' -v

# 四类错误与优先级
go test ./temporal -run 'TestError|TestNoPartial' -v

# 并发写入下的快照一致性与可串行化（竞态检测）
go test -race ./temporal -run TestConcurrent -v

# 随机操作序列与独立朴素模型逐条对照
go test ./temporal -run TestDifferential -v

# 单边判定开销不随链接类型历史增长（探针计数可独立验证）
go test ./temporal -run 'TestLinkDecision|TestProbeCount|TestNaiveOracle' -v

# 审计留痕
go test ./temporal -run TestAudit -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
