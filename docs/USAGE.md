# 使用指南：子图快照提取

## 快速开始

```go
g := ontology.NewGraph()

// 1) 建对象与链接（链接两端必须已存在，主图永不悬挂）
g.AddObject(ontology.Object{ID: "a", Type: ontology.ObjectType{Name: "doc"}})
g.AddObject(ontology.Object{ID: "b", Type: ontology.ObjectType{Name: "doc"}})
g.AddLink(ontology.Link{
    ID:   "l1",
    Type: ontology.LinkType{Name: "refs", Directed: true},
    From: "a", To: "b",
})

// 2) 授予存在性权限
g.GrantExistence("alice", "a")
g.GrantExistence("alice", "b")

// 3) 提取快照
snap, err := ontology.NewExtractor(g).Extract("alice", []ontology.ID{"a", "b"})
```

## 结果形态

- `snap.Revision`：快照对应的主图唯一时点；
- `snap.Objects` / `snap.Links`：纳入本体（保证每条链接两端都在 `Objects` 中）；
- `snap.Dangling`：边界/权限剔除产生的悬挂链接附属信息；
- `snap.DanglingByLink(id)`、`snap.HasDangling(id)`：查询某条被排除链接；
- 每条 `DanglingLink` 的 `Source` 为 `DanglingBoundary`（"boundary"）或
  `DanglingPermission`（"permission"），`Dir` 为从 `Included` 端观察到的方向。

## 错误与退化

| 情形 | 结果 |
| --- | --- |
| 空范围 / 非法标识 / 超上限 | `ErrEmptyScope` / `ErrInvalidID` / `ErrScopeTooLarge`（均为 `IllegalScopeError`） |
| 剔除后有效集合为空 | 空快照，`error == nil` |
| 正常 | 完整快照 |

判定次序固定为 参数非法 > 剔除至空 > 正常提取。

## 差异与归因

```go
d := ontology.Compare(snapBefore, snapAfter)
attr := g.Attribute(snapBefore, snapAfter) // 修订窗口内的具体变更
```

`d.Empty()` 为真时两次快照内容一致；非空差异应能由 `attr.Changes` 解释。
