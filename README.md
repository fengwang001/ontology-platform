# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## ABAC 访问控制（`abac` 包）

`abac` 包实现基于属性的访问控制（Attribute-Based Access Control），判定输入为三类属性与动作：

- 主体属性 `Subject`：如角色、部门、用户 ID。
- 资源属性 `Resource`：如资源类型、属主、密级；`nil` 表示资源不存在/不可见。
- 环境属性 `Environment`：如来源 IP、时间段。

一条 `Policy` 由主体/资源/环境三组 `Condition` 与一个效果（`allow`/`deny`）组成，
三组条件全部匹配（逻辑与）策略才命中。条件算子包括：`eq`、`ne`、`exists`、`not_exists`、
`contains`、`prefix`、`suffix`、`lt`、`le`、`gt`、`ge`、`in`。

### 组合与判定规则

1. **拒绝优先（deny-overrides）**：只要有一条 `deny` 策略命中，结果即为拒绝；
   即使同时有 `allow` 策略命中也不例外。审计结果中同时记录命中的拒绝策略与被覆盖的允许策略。
2. **缺失属性不匹配，而非当作假值**：策略引用的属性在请求中缺失时，该策略进入
   “无法求值（indeterminate）”状态，不参与命中组合；若存在无法求值的策略且没有 `deny` 命中，
   请求以 `missing_referenced_attribute` 拒绝，避免把缺失误判成条件为假而放行。
   显式使用 `exists`/`not_exists` 算子检查属性存在性时，缺失本身就是可判定的合法结果。
3. **默认拒绝**：没有任何 `allow` 命中即拒绝。
4. **不泄露资源存在性**：返回给调用方的 `Decision` 在所有拒绝场景下使用统一文案
   `access denied`，不包含原因码、策略 ID 或资源是否存在的信息；“资源不存在”和
   “资源存在但无权访问”对外完全一致。可区分的原因（`deny_by_policy`、
   `missing_referenced_attribute`、`no_applicable_policy`、`invalid_request`）
   仅出现在服务端 `AuditDecision` 与审计日志中。
5. **确定性与并发安全**：`Evaluate` 只读，判定不会改变策略集合（被拒绝的操作同样不产生任何写入）；
   策略按 ID 排序后求值，同一组策略以任意顺序注册、并发判定同一请求，都得到完全一致的结果。

### 审计日志

通过 `WithLogger` 注入日志器。内置 `SlogLogger`（基于 `log/slog`）在每次判定时打印：
主体、资源、动作、最终允许/拒绝、原因码、命中的拒绝/允许策略、无法求值的策略以及判定依据说明。

```go
eng := abac.NewEngine(abac.WithLogger(abac.NewSlogLogger()))
_ = eng.Register(abac.Policy{
    ID:      "deny-admin",
    Effect:  abac.EffectDeny,
    Subject: []abac.Condition{{Key: "role", Op: abac.OpEqual, Value: "admin"}},
})
pub, audit := eng.Evaluate(ctx, abac.Request{
    Action:   "read",
    Subject:  abac.Attributes{"role": "admin"},
    Resource: abac.Attributes{"kind": "report"},
})
// pub.Message 恒为 "access denied"；audit.Reason 为 deny_by_policy
_ = pub
_ = audit
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

# 仅 ABAC 包（含竞态检测与详细输出）
go test -race -v ./abac

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
