# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 权限变更生效时序模块（`temporal`）

提交时刻与生效时刻双轴的权限规则管理，支持排队变更撤回、已生效变更的
追溯撤销（沿依赖边传递重估）、任意历史时刻可重复读访问判定。

- 设计与取舍：`docs/design.md`
- 核心实现：`temporal/engine.go`、`temporal/replay.go`
- 朴素参照实现：`temporal/naive.go`（随机差分测试基准）
- 审计日志：`temporal/audit.go`（JSON Lines：输入 / 输出 / 错误 / 时序依据）

```go
eng := temporal.NewEngine(0, temporal.NewAuditLog(logFile))

eng.Submit(ctx, temporal.Change{
    ID: "g1", Subject: "u1", Label: "doc",
    Kind: temporal.Grant, Committed: 5, Effective: 10,
})
d, _ := eng.Decide(ctx, "u1", "doc", 12) // 12 时刻的确定判定
d.Allowed        // 判定结果
d.Basis          // 据以裁决的生效时序依据
d.Examined       // 截至该时刻对该主体/标签确实生效过的变更数

eng.Withdraw(ctx, "g2", 6)  // 仅允许撤回尚未生效的排队变更
eng.Submit(ctx, temporal.Change{ // 已生效变更只能通过新变更追溯撤销
    ID: "r1", Kind: temporal.Revoke, Target: "g1",
    Committed: 11, Effective: 12,
}) // 返回 Reassessment：受牵连后续变更在新基线下的确定结论
```
