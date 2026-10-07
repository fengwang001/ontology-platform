# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 聚合视图子系统

`aggview` 提供跨对象类型、沿固定长度链接路径传播的最大值聚合视图：
对起点类型的每个实例，汇总其沿路径恰好 k 跳可达的全部**不同**终点实例
在某数值属性上的最大值；无可达终点时返回明确的不存在状态（`Absent`）。

- 链接任意一跳增删 → 精确识别受影响起点并增量维护，不多算、不遗漏。
- 终点属性变小且恰为最大值来源 → 只在该起点当前可达终点范围内重新定最大值。
- 重复到达（不同中间组合 / 并行链接）只计一次；按层去重 BFS 正确处理环。
- 单把读写锁保证并发链接变更与属性写入的串行等价；查询只读不改状态。
- 支持某一跳允许类型集合的结构性新增成员，不破坏既有连通关系。
- 固定优先级的四类错误：类型不匹配 > 实例不存在 > 运行时环 > 维护失败回滚。

设计取舍、被放弃方案与对拍模型见 `docs/DESIGN.md`。

### 最小用例

```go
st := ontology.NewStore()
st.EnsureType("A"); st.EnsureType("B"); st.EnsureType("C")
en := aggview.NewEngine(st)
_ = en.DeclareView(aggview.PathDecl{
    Name:   "v",
    Source: "A",
    Hops: []aggview.HopDecl{
        {Relation: "r1", Types: []string{"B"}},
        {Relation: "r2", Types: []string{"C"}},
    },
    Target: "C",
    Attr:   "score",
})
st.CreateObject("a", "A", nil)
// ... 创建 b、c 并写入 score ...
_, _ = en.AddLink(ontology.Link{ID: "l1", From: "a", Rel: "r1", To: "b"})
r, _ := en.Query("v", "a") // r.Absent 或 r.Max
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
