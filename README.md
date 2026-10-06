# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 岗位编制与录用通知

核心实现位于根包：

- `service.go`：服务状态、岗位、候选人、时钟、编制调整与只读快照。
- `offers.go`：单份与批量录用通知、薪资带宽、例外额度、冷却和候选人有效通知索引。
- `lifecycle.go`：答复、惰性过期、放弃、撤回、协商取消、入职与离职。
- `errors.go`：可区分的错误码和批量失败下标。
- `DESIGN.md`：不变量、并发取舍、复杂度证明与朴素模型对照方法。

最小用法：

```go
svc := staffing.NewService(cooldownDays, graceDays)
_ = svc.AddCandidate(0, "candidate-1")
_ = svc.AddPosition(0, staffing.Position{
    ID:        "backend-l5",
    Level:     "L5",
    MinSalary: 100,
    MaxSalary: 150,
    Total:     3,
    Status:    staffing.PositionOpen,
})

_, _ = svc.IssueOffer(staffing.IssueOfferInput{
    Now:         1,
    OfferID:     "offer-1",
    CandidateID: "candidate-1",
    PositionID:  "backend-l5",
    Salary:      130,
    Deadline:    10,
})
_ = svc.RespondToOffer(8, "offer-1", true, 12)
_ = svc.Onboard(12, "offer-1")
```

错误码按需求优先级短路返回：`invalid_argument`、`clock_rolled_back`、`not_found`、`status_not_allowed`、`position_frozen`、`headcount_full`、`salary_band_without_approval`、`candidate_has_pending_offer`、`cooling_down`、`offer_expired`。批量失败通过 `*staffing.Error` 的 `Index` 暴露最小失败下标。

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

本仓库在当前容器中如遇只读默认缓存，可使用：

```bash
GOCACHE=/tmp/go-cache go test -race -v ./...
GOCACHE=/tmp/go-cache go vet ./...
GOCACHE=/tmp/go-cache go test -run '^$' -bench BenchmarkIssueLookupWithLargeHistory -benchtime=100x ./...
```

随机对照测试带固定种子并打印每步输入、输出和判定依据；失败时可用 `go test -run TestRandomOperationsMatchNaiveModel -v` 复现。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
