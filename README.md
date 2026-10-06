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

## 物业费账单服务（`billing/`）

滞纳金累计、分级催缴与缴款冲抵服务。设计说明见 `docs/design.md`。

```go
cfg := billing.Config{
    GraceDays: 3,
    RateNum: 1, RateDen: 10,   // 每日按未付本金的 10% 累计滞纳金
    CapNum: 1, CapDen: 2,      // 滞纳金上限为本金的 50%
    Thresholds: [3]int{5, 10, 15}, // 三个催缴阶段的逾期天数阈值
}
svc, _ := billing.NewService(cfg)
svc.GenerateBill(now, "household-1", "bill-1", 1000, dueDay)
res, _ := svc.Pay(now, "household-1", 500)            // 部分缴款，自动冲抵
svc.Dispute(now, "household-1", "bill-1")             // 争议：停计、冻结、跳过
svc.ResolveDispute(now, "household-1", "bill-1", 800) // 裁定：维持或调减本金
svc.Waive(now, "household-1", "bill-1", 20)           // 减免已产生的滞纳金
due, _ := svc.TotalDue(now, "household-1")            // 应付总额（只扫未关账账单）
```

错误按固定次序只报第一个：参数非法 → 时钟回退 → 住户或账单不存在 →
状态不允许 → 金额越界；被拒绝的操作不改变任何状态（含时钟）。
