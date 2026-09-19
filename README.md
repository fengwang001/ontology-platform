# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）——关系（Link）子系统。
仅使用 Go 标准库，状态保存在进程内存中，不依赖任何外部服务。

## 环境要求

- Go 1.26+（`go version` 确认）

## 快速上手

```go
s := ontology.NewStore()

// 1. 注册 ObjectType 并添加对象
_ = s.RegisterObjectType("User")
_ = s.RegisterObjectType("Doc")
_ = s.AddObject("User", "u1")
_ = s.AddObject("Doc", "d1")

// 2. 声明 LinkType
_ = s.DeclareLinkType(ontology.LinkType{
    Name:           "owns",
    SourceType:     "User",
    TargetType:     "Doc",
    Cardinality:    ontology.ManyToMany,
    SourceRequired: false,             // 源侧是否必选（提交时校验）
    TargetRequired: false,             // 目标侧是否必选（提交时校验）
    Cascade:        ontology.CascadeDelete,
})

// 3. 建链 / 断链 / 双向遍历
_ = s.Link("owns", ontology.ObjectKey{Type: "User", ID: "u1"},
    ontology.ObjectKey{Type: "Doc", ID: "d1"})
targets := s.LinksFrom("owns", ontology.ObjectKey{Type: "User", ID: "u1"}) // 正向
sources := s.LinksTo("owns", ontology.ObjectKey{Type: "Doc", ID: "d1"})    // 反向
```

## LinkType 声明方式

每个 `LinkType` 包含：

- `Name`：唯一名字。
- `SourceType` / `TargetType`：源与目标 ObjectType，可以是同一个（自引用）。
- `Cardinality`：`OneToOne` / `OneToMany` / `ManyToMany`。
- `SourceRequired` / `TargetRequired`：源侧 / 目标侧必选性，仅在 `Tx.Commit` 时校验。
- `Cascade`：删除任一侧端点对象时的级联语义，见下文。

## 五类违约的判定规则

建链时即时校验，五类错误可通过 `errors.As` 分别判定，错误中均携带
LinkType 名、源端点、目标端点与违约类别：

| 违约场景 | 错误类型 | 判定方式 |
| --- | --- | --- |
| ONE_TO_ONE 源已有链 / 目标被占用 | `*CardinalityError` | `Kind == ViolationOneToOneSource / ViolationOneToOneTarget` |
| ONE_TO_MANY 目标已归属其他源 | `*CardinalityError` | `Kind == ViolationOneToManyTarget` |
| 端点 ObjectType 与声明不符 | `*EndpointTypeError` | 含期望的源/目标类型 |
| 端点对象不存在 | `*ObjectNotFoundError` | `Side` 为 `source` / `target` |
| 重复建立同一条链 | `*DuplicateLinkError` | — |

必选性违约（`SourceRequired` / `TargetRequired`）只在 `Tx.Commit` 时校验，
通过 `*RequiredError.Violations` 一次报出全部缺失的必选关系，且提交整体回滚。

## 三种级联语义的结算顺序

`DeleteObject` 先规划后执行：以被删对象为根做 BFS，沿每条链按 LinkType
声明的语义结算，规划全部成功后才真正落库，因此 RESTRICT 触发时零副作用。

1. `CascadeDelete`：对端对象入队连带删除，并继续触发它自身的级联（环上
   通过去重保证每个对象只结算一次，必然终止）。
2. `CascadeSetNull`：只断开链，对端对象保留。
3. `CascadeRestrict`：对端不会被同次级联删除时拒绝整次删除，已规划的连带
   删除与断链全部回滚；`*RestrictError.Path` 给出从被删对象到该 RESTRICT
   链的完整路径。

## 批量与事务

- `ApplyBatch(ops)`：混合建链、断链、删除对象，整批原子生效或全部回滚；
  失败时 `*BatchError.Index` 指出批内第几条操作。批内语义与逐条调用原语
  一致：重复建同一条链报错、先建后删净效果为空、批内建立的链会被同批的
  级联删除一并结算。
- `Begin() -> Tx`：与批量相同，但 `Commit` 时额外校验必选性约束。

## 索引不变量自检

```go
violations := s.CheckInvariant() // 空切片表示正反索引互为镜像
for _, v := range violations {
    // v.LinkType / v.Side("forward"|"reverse"|"dangling") / v.Source / v.Target
}
```

## 环检测

```go
cycles := s.FindCycles(start, []string{"depends"}) // [][]PathStep，去重且排序稳定
```

## 并发安全

全部方法并发安全（内部读写锁）。任何时刻从外部观察到的正反索引互为镜像；
基数约束不会被并发穿透（如两个 goroutine 同时给同一 ONE_TO_ONE 源建链，
恰好一个成功，另一个收到 `*CardinalityError`）。

## 测试

```bash
go test -count=1 ./...
go test -race -count=1 ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
