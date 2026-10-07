# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 并发写冲突判定与自动合并

对象类型的每个属性可分别声明是否允许并发写冲突时自动合并：

- 可合并属性声明合并规则（`max_int` / `min_int` / `set_union` / `lww`），
  合并结果确定且与写入到达顺序无关；
- 不可合并属性并发写新值不同时，较晚判定的那次写入被**整条原子拒绝**；
- 基线版本冲突、不可合并属性冲突、合并后提交三种结果互斥，
  基线冲突优先判定；新值恰好相同的并发写视为等价写，均判成功；
- 冲突判定基于每属性版本号，开销 O(触及属性数)，与历史版本总数无关；
- 全部判定记录于可独立重放核验的日志。

设计与取舍详见 [DESIGN.md](DESIGN.md)。

### 快速示例

```go
typ := &ontology.ObjectType{
    Name: "ticket",
    Properties: []ontology.PropertySpec{
        {Name: "status", Mergeable: false},
        {Name: "priority", Mergeable: true, MergeRule: ontology.RuleMaxInt},
    },
}
store := ontology.NewStore()
store.CreateInstance("T1", typ, map[string]ontology.Value{
    "status": "open", "priority": int64(0),
})
res, _ := store.Apply(ontology.WriteRequest{
    RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
    Changes: map[string]ontology.Value{"status": "closed"},
})
// res.Outcome ∈ committed / rejected_stale_baseline / rejected_conflict
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
