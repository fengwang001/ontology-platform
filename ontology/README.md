# 权限传播网关（ontology package）

主体对对象类型的授权沿声明了传播能力的**有向链接类型**扩散，支持每条链接
独立的最大传播深度、目标类型的两种覆盖（替换 / 阻断）、传播环路整体拒绝，
以及与规模无关的判定成本。完整设计、取舍与证明见 `../ontology/design.md`。

## 快速开始

```go
import "ontology/ontology"

gw := ontology.NewGateway(ontology.WriteLogger{W: os.Stdout})
_ = gw.AddObjectTypes(ctx, "Project", "Folder", "Doc")

_ = gw.UpsertLink(ctx, ontology.LinkType{
    Name: "contains", From: "Project", To: "Folder", PropagationDepth: 2,
})
_ = gw.ApplyGrant(ctx, ontology.Grant{
    Subject: "alice", Object: "Project", Action: "read", Effect: ontology.Allow,
})

d, err := gw.Decide(ctx, "alice", "Folder", "read")
// d.Allowed == true, d.Basis 记录命中的传播路径
```

## 原子变更

单次便捷方法（`UpsertLink`、`ApplyGrant`、`SetOverride` …）本身也是一次原子
提交；多步组合使用 `Gateway.Apply`，整批要么全部生效、要么全部不生效：

```go
err := gw.Apply(ctx,
    ontology.Change{Kind: ontology.UpsertLinkChange, Link: l1},
    ontology.Change{Kind: ontology.ApplyGrantChange, Grant: g1},
)
```

## 覆盖

- `ontology.ReplaceOverride`：替换上游传播并集，只保留该类型自身直接授权；
  自身授权可继续向下游传播。
- `ontology.BlockOverride`：替换并彻底阻断继续传播（含自身授权的继续传播）。

## 错误与拒绝原因

配置问题返回 `*ontology.GatewayError`，其 `Kind` 为
`KindObjectNotFound` / `KindPropagationCycle` / `KindInvalidDepth` /
`KindConflictingOverride`，彼此可区分。普通无权限不是错误，体现在
`Decision.Allowed == false` 与 `Decision.DenyReason`
（`DenialExplicit` / `DenialBlocked` / `DenialReplaced` /
`DenialDepth` / `DenialNone`）。

## 判定日志

传入任意实现 `ontology.Logger` 的记录器即可逐次打印“输入、输出、依据”；
`ontology.WriteLogger` 输出单行文本。

## 测试与演示

```bash
go test -race ./...
go run ./cmd/server
```
