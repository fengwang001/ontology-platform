# 使用指南

包路径：`ontology`（module `ontology`）。

## 1. 建库与追加事件

```go
st := ontology.New()

st.Append(ontology.EventInput{Kind: ontology.EvLinkTypeDeclared, Time: 0, TypeID: "Parent"})
st.Append(ontology.EventInput{Kind: ontology.EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "ParentObj"})
st.Append(ontology.EventInput{Kind: ontology.EvObjectCreated, Time: 1, ObjectID: "c", TypeID: "Child"})
st.Append(ontology.EventInput{Kind: ontology.EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: "Parent"})
st.Append(ontology.EventInput{Kind: ontology.EvLinkRevoked,    Time: 10, ObjectID: "p", PeerID: "c", TypeID: "Parent"})
```

边方向为 `ObjectID -> PeerID`；必需性按入链（边的 `To`）解释。
`Append` 自动分配全局 `Seq`。外部已编号日志用 `ImportStream`；相同
`(Time, Seq)` 会保留并在判定时报 `ErrAmbiguousOrder`。

## 2. 安装规则版本

```go
// 追溯版：评估 t>=2 起的全部历史撤销
err := st.AdjustRule(ontology.RuleVersion{
    ID: "R1", EffectiveFrom: 2, EffectiveFromSeq: 1,
    Retroactive: true,
    Requirement: map[string][]string{"Child": {"Parent"}},
})

// 之后只能安装排序更晚的新版本（不可变、不回退）
_ = st.AdjustRule(ontology.RuleVersion{
    ID: "R2", EffectiveFrom: 50, EffectiveFromSeq: 2,
    Retroactive: false,
    Requirement: map[string][]string{"Child": {"Parent"}},
})
```

## 3. 发起孤儿追溯判定

```go
// 钉死不可变版本：任何时候调用，对同一 T 结论恒等（幂等）
res, err := st.Determine(ontology.DetermineRequest{
    ObjectID: "c", AtTime: 12,
    Basis: ontology.Basis{VersionID: "R1"},
})

// HEAD 版：若处理期间 HEAD 被新版本替换，返回 ErrRuleSuperseded
res, err = st.Determine(ontology.DetermineRequest{ObjectID: "c", AtTime: 12})
```

结果字段：

- `res.Status`：`StatusActive` / `StatusCascadeOrphan` / `StatusRetroactiveOrphan`；
- `res.VirtualOrphanAt`、`res.HasVirtualPoint`：追溯孤儿的虚拟孤儿点；
- `res.MarkedOrphanAt`、`res.HasMarkedRecord`：流中显式级联记录；
- `res.ActivityAfterVirtual`：虚拟点之后仍发生的属性赋值 / 新链接；
- `res.EventsScanned`：本次索引扫描条数（不随全网事件量增长）；
- `res.CrossCheck`：独立朴素重放的对照结论与是否一致。

## 4. 全网重建

```go
net, err := st.Rebuild(12, ontology.Basis{VersionID: "R1"})
for id, obj := range net.Objects {
    _ = id
    _ = obj.Status
    _ = obj.ActivityAfterVirtual
}
// 单个对象的判定错误不影响其它对象：
for id, prob := range net.Problems { _ = id; _ = prob }
```

## 5. 错误处理

```go
res, err := st.Determine(req)
if de, ok := ontology.AsDeterminationError(err); ok {
    switch de.Kind {
    case ontology.ErrRuleSuperseded:        // 改为显式钉版后重试
    case ontology.ErrDanglingReference:
    case ontology.ErrBeforeFirstAppearance:
    case ontology.ErrAmbiguousOrder:
    }
}
```

多类错误同时成立时按固定优先级只返回一类：
`ErrRuleSuperseded > ErrDanglingReference > ErrBeforeFirstAppearance > ErrAmbiguousOrder`。
错误不追加事件、不改规则、不写审计。

## 6. 审计核查

```go
for _, rec := range st.AuditLog() {
    _ = rec.ObjectID
    _ = rec.AtTime
    _ = rec.BasisResolved
    _ = rec.RuleVersionID
    _ = rec.CrossCheck.Agrees
}
```
