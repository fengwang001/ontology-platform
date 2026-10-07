# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 批量更新能力

`ontology` 包支持跨对象类型、多实例的**原子批量更新**：一批 `Batch`
要么整体生效，要么整体不生效，生效前后外部读取都观察不到中间状态。

- 四类互斥失败，固定优先级：重复声明 > 版本冲突 > 校验钩子 > 最终基数。
- 逐实例核对调用方声明的基线版本（不存在记为 0），允许同批使用不同基线。
- 链接基数在“整批最终链接状态”上一次性校验（含悬空链接、端点类型）。
- 失败不修改任何版本、链接或逻辑时钟；成功整体切换并推进一个逻辑 tick。
- 决策工作量只与批次相关实例数有关，与系统实例总数无关（`Stats` 计数器
  为证，见 `TestDecisionCostIndependentOfStoreSize`）。
- 每次尝试（含失败）完整记入可重放的 `Journal`（可选 JSONL 落盘）。

最小用例：

```go
p := ontology.NewPlatform()
p.RegisterObjectType(ontology.ObjectType{
    Name: "Person",
    Hook: func(cur, prop ontology.Properties) error { return nil },
})
p.RegisterLinkType(ontology.LinkType{
    Name: "membership", LeftType: "Person", RightType: "Group",
    CardA: ontology.Cardinality{Min: 0, Max: 2},
    CardB: ontology.Cardinality{Min: 1, Max: 2},
})
p.SetJournal(ontology.NewJournal("/tmp/ontology.jsonl"))

r := p.Commit(ontology.Batch{
    ID: "b1",
    Ops: []ontology.Operation{
        {Instance: "alice", Type: "Person", BaseVersion: 0,
            Props: ontology.Properties{"name": "Alice"}},
    },
    Links: []ontology.LinkOp{
        {Link: "membership", A: "alice", B: "g1", Add: true},
    },
})
// r.OK, r.Failure (FailureDuplicateWrite/Conflict/HookRejected/Cardinality),
// r.NewVersions, r.CommitTick
```

读取：`p.Read(id)`、`p.ReadMany(ids...)`（单次多读共享同一 tick）。
设计取舍、放弃方案与验证细节见 [`docs/DESIGN.md`](docs/DESIGN.md)。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 当前交付为库包 ontology/（无独立服务进程）
go build ./...
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
