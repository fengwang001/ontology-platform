# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 住宅租约续签服务

根包提供 `lease.Service`，所有日期是整数日序号，租金是整数分：

- `CreateLease`：登记租约、当前月租金和上次调价生效日。
- `IssueOffer` / `WithdrawOffer`：房东在 `[end-B,end-A]` 发出或撤回续签要约。
- `TenantRespond`：租户在 `[issued,issued+C]` 接受、拒绝或提出一次反要约。
- `LandlordRespondToCounter`：房东在反要约提出后 `C` 天闭区间内接受或拒绝。
- `GiveTerminationNotice`：按月延续期间任一方通知，`now+D` 生效。
- `Snapshot`：按调用传入的 `now` 返回 active、protected、month_to_month 或 terminated 视图。

错误依次为：`invalid_argument`、`clock_rollback`、`lease_not_found`、
`invalid_state`、`rent_above_cap`、`late_response`。被拒绝操作不写状态、
不推进逻辑时钟。涨幅以整数分计算，不足一分的额度舍去。

设计取舍见 `DESIGN.md`；固定分界测试见 `service_test.go`，并发/复杂度证明见
`concurrency_test.go`，独立朴素模型与 80×120 步随机重放见 `naive_test.go`。

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
