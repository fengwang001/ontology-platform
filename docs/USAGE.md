# 使用指南

## 核心类型

- `ont.ActionDeclaration`：一次动作的声明。
  - `Direct []DirectOp`：直接目标操作（`read/update/delete/create`）。
  - `Cascades []CascadeRule`：沿链接类型的级联（方向、效果、`NoPropagate`）。
  - `MaxDepth int`：唯一的传播深度上限（根为深度 0）。
  - `InvisibleMode`：`RejectOnInvisible` 或 `SkipOnInvisible`（互斥）。
  - `Merge`：`MergeAll` 或 `MergeAny`。
- `ont.Decider`：实现两个方法
  - `Visible(subject, inst, depth) bool`
  - `Allowed(subject, inst, op, depth) (allowed bool, abstain bool)`
- `ont.MemStore`：内存存储，自带事务暂存区。

## 最小示例

```go
store := ont.NewMemStore()
store.AddInstance(ont.Instance{ID: "A", Type: "doc", Version: 1})
store.AddInstance(ont.Instance{ID: "B", Type: "doc", Version: 1})
store.AddEdge(ont.Edge{LinkType: "rel", From: "A", To: "B"})

eng := ont.NewEngine(store, myDecider, ont.NewJSONLogger(os.Stdout))
rep, err := eng.Execute(ont.ActionDeclaration{
    Name:    "edit",
    Subject: "user-1",
    Direct:  []ont.DirectOp{{Op: ont.OpUpdate, Type: "doc", Target: "A",
        NewAttrs: map[string]string{"title": "x"}}},
    Cascades:      []ont.CascadeRule{{LinkType: "rel", Outgoing: true, Effect: ont.OpUpdate}},
    MaxDepth:      2,
    InvisibleMode: ont.RejectOnInvisible,
    Merge:         ont.MergeAll,
})
```

## 结果解读

- `err == nil && rep.Committed == true`：作为一个事务原子生效。
- `err.(*ont.ActionError).Kind`：按固定优先级给出拒绝类别
  （`RejectInvalidDeclaration` → `RejectDepthExceeded` →
  `RejectDirectInvisible` → `RejectCascadeInvisible` → `RejectDenied`）。
- `rep.Path`：每个被触及实例的层级、经由链接与裁决依据。
- `rep.Skipped`：跳过模式下因不可见被跳过的实例及影响范围（不含属性）。
- `rep.Checks`：本次可见性 + 授权检查总次数（规模无关性的可观测证据）。
