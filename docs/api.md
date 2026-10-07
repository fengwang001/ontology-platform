# API 参考

包路径：`ontology`

## 建模

- `ObjectType{Name}`：对象类型。
- `LinkType{Name, Source, Sink, Direct}`：链接类型；`Direct` 取 `Directed`（有向，
  Source→Sink）或 `Undirected`（无向，存储时端点规范化排序，`{A,B}` 与 `{B,A}`
  等价）。添加链接实例时两端对象类型必须匹配；无向类型允许以相反端点顺序实例化。
- `Object{ID, Type, Readers}`：对象实例；`Readers` 是拥有存在性权限的调用者集合。
- `Link{Type, Source, Sink}`：链接实例。

## 主图变更（均并发安全）

- `NewGraph() *Graph`
- `AddObjectType` / `AddLinkType`
- `AddObject` / `RemoveObject`（删除对象级联删除其全部关联链接）
- `AddLink` / `RemoveLink`
- `Epoch() uint64`：当前版本号；每次成功变更 +1。
- `Journal() []Change`：只增变更日志（防御性拷贝），`Change{Epoch, Kind, Object,
  Link}`，`Kind` 为 `ObjectAdded/ObjectRemoved/LinkAdded/LinkRemoved`。

错误哨兵：`ErrInvalidArgument`、`ErrNotFound`、`ErrTypeMismatch`（用 `errors.Is`）。

## 提取

```go
snap, err := g.Extract(caller, scope)
```

判定次序：参数非法（错误）→ 剔除后为空（合法空快照）→ 正常提取。

`Snapshot`：

- `Epoch`：快照对应的唯一主图时点；
- `Objects []Object`：纳入对象，按 id 排序；
- `Links []Link`：两端均被纳入的链接，按 `(type, source, sink)` 排序；
- `Dangling []DanglingLink`：恰好一端纳入的边界链接附属信息，确定顺序排序。

`DanglingLink`：

- `Type`、`LinkDirection`；
- `LocalEnd`：被纳入的一端；
- `RemoteEnd` / `RemoteRedacted`：另一端；调用者对其无存在性权限时 id 脱敏置空；
- `Source`：`DanglingByScope`（远端从未在 scope 中，范围边界优先）或
  `DanglingByPermission`（远端在 scope 中但因无权限被剔除）。

不变量：`Links` 中每条链接的两端必然都在 `Objects` 中；自环永不悬挂；同
(scope, caller, epoch) 的提取结果完全一致。
