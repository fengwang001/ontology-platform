# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：银行卡拒付案件管理与商户资金扣回

- `chargeback/`：拒付全生命周期引擎（提起 → 应诉 → 发卡行审阅 →
  预仲裁 → 终局裁决），惰性逾期认定，O(1) 资金查询。
- `naive/`：独立朴素模型，仅用于随机对照测试。
- 设计取舍、被放弃方案与验证方法见 [DESIGN.md](DESIGN.md)。

### 快速示例

```go
e := chargeback.New(chargeback.Config{
    FraudWindowDays: 30, NotReceivedWindowDays: 45, DuplicateWindowDays: 60,
    DuplicateMatchDays: 10, ResponseWindowDays: 7, ReviewWindowDays: 5,
    ArbitrationFee: 15,
})
_ = e.AddTransaction(10, chargeback.Transaction{
    ID: "t1", SettleDay: 10, Amount: 10000, CardID: "card-1", MerchantID: "mch-1",
})
_ = e.OpenDispute(20, "case-1", "t1", chargeback.ReasonFraud, 5000) // 即刻扣回
_ = e.Respond(25, "case-1")                                          // 商户应诉
_ = e.PreArbitrate(28, "case-1")                                     // 发卡行预仲裁
_ = e.Rule(40, "case-1", chargeback.OutcomeMerchantWin)              // 终局裁决
bal, _ := e.MerchantBalance(40, "mch-1")
```

### 规则要点

- 时间为整数天，所有操作与查询携带 `now`，不得早于引擎当前时刻，
  被拒绝的操作不改变案件、资金与时钟。
- 提起窗口 / 应诉期 R / 审阅期 P / 重复判定天数均为“恰等有效、次日
  逾期”；逾期自动认定无需任何操作触发。
- 错误可区分（`chargeback.Error.Code`），并按固定优先级只报第一个。
- 资金守恒：`Σ商户余额 + 发卡行账 + 待决扣回款 + 累计仲裁费 = 0`，
  可用 `LedgerSummary` 随时校验。

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
