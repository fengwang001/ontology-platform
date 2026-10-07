# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 快照差异比对组件

对平台在两个不同时点产生的快照做差异比对：先结构层（对象类型及
属性的增删改，重命名与改型为独立维度），再实例层（对象与链接的
创建、删除、取值变化，更新精确到属性）。详见
[docs/diff-design.md](docs/diff-design.md)。

```go
res, err := ontology.Compare(oldSnap, newSnap)
// res.Schema    结构层差异
// res.Instances 实例层差异
// res.Stats     工作量指标（用于复核开销只与真实差异相关）
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个用例
go test -run TestWorkIsIndependentOfUnchangedCount -v .

# 随机对照（主实现 vs 朴素参照模型），可放大规模并落盘记录
ONTDIFF_ITERATIONS=3000 go test -run TestRandomPairsAgainstNaive .
ONTDIFF_RECORD=/tmp/records.jsonl go test -run TestRandomPairsAgainstNaive .

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
