# 使用指南

```go
import "ontology/ontology"

store := ontology.NewStore()

// 注册对象类型与校验钩子。Declare 声明本次写入“会读到”的字段（含跨实例），
// 这些字段自动并入冲突判定的相关集合；Validate 做实际业务校验。
store.RegisterType("Order", ontology.Validator{
    Name: "check-customer-status",
    Declare: func(vals map[ontology.Property]ontology.Value, snap ontology.Snapshot) ontology.ReadScope {
        return ontology.ReadScope{
            Refs: []ontology.PropertyRef{
                {Local: "amount"},                         // 读本实例 amount
                {Link: "customer", Local: "status"},        // 经由 customer 链接读其 status
            },
        }
    },
    Validate: func(vals map[ontology.Property]ontology.Value, snap ontology.Snapshot,
        linked map[ontology.ObjectID]ontology.Snapshot) error { return nil },
})

// 创建（基线 0）。
r := store.Commit(ontology.WriteRequest{
    Object: "order-1", Type: "Order", Create: true,
    Values: map[ontology.Property]ontology.Value{"amount": 10.0, "customer": "cust-7"},
    ObserveExternal: map[ontology.ObjectID]ontology.Version{"cust-7": 3},
})

// 读取快照（任意版本，字节不可变）。
snap, ok := store.Read("order-1", r.NewVersion)

// 基于已读版本更新：属性不相交的并发写会合并；相交或 hook 读范围被触碰则冲突。
r2 := store.Commit(ontology.WriteRequest{
    Object: "order-1", Type: "Order", Base: snap.Version(),
    Values: map[ontology.Property]ontology.Value{"note": "rush"},
})

// 结果区分。
switch {
case errors.Is(r2.Err, ontology.ErrInstanceDeleted): // ConflictDeleted
case errors.Is(r2.Err, ontology.ErrStaleBase):       // ConflictStaleBase（含幂等重复）
case errors.Is(r2.Err, ontology.ErrPropertyConflict):// ConflictProperty
case errors.Is(r2.Err, ontology.ErrRejected):        // hook 业务校验拒绝
}

// 审计/重放：完整记录写集合、读范围、证据比较与裁定。
for _, d := range store.Decisions("order-1") { _ = d }

// 逻辑删除后，对该实例的一切写入优先返回 ConflictDeleted。
store.Commit(ontology.WriteRequest{Object: "order-1", Base: r2.NewVersion, Delete: true})
```

幂等：相同 `(Object, IdempotencyKey)` 的重复提交返回 `ConflictStaleBase`，
不产生新版本、不推进时钟。
