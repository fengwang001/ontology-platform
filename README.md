# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## settlement 包：延迟结算与滚动保证金

`settlement/` 实现支付商户按营业日的延迟结算、按比例滚动留存保证金、到期释放
与负净额动用保证金。设计与取舍见 [`DESIGN.md`](DESIGN.md)，包级语义见
`settlement/doc.go`。

快速示例：

```go
sys, err := settlement.NewSystem([]Day{0, 1, 2, 3, 4})

// N=2 延迟，10%（1000bp）留存，H=3 营业日后释放
err = sys.AddMerchant(4, "M0", MerchantConfig{
    SettleDelayN: 2, ReserveBps: 1000, ReserveHorizonH: 3,
})

// 记一笔支付（金额单位为最小货币单位整数）
err = sys.AddTransaction(4, "M0", Transaction{ID: "tx-1", Day: 2, Amount: 10000})

// 一次追赶结算到营业日 4（内部逐日产出记录，可与逐天结算互换）
payouts, details, err := sys.Settle(4, "M0", 4)

st, err := sys.State("M0")
// 不变量：st.TotalPayout + st.ReserveBalance + st.NegativeCarry
//         == 全部已处理流水净额
```

行为要点：

- 流水发生日晚于 `now` 报日期非法；早于最近已结算营业日报已封账。
- `now` 全局单调，回退报时钟回退；被拒绝的操作不改变任何状态与时钟。
- 每个营业日产生一条出款记录（含零出款）；负余额结转不被丢弃，只由后续正净
  额或到期释放清偿。
- 批次到期只释放未动用余额；同一营业日不会同时新留存与动用。

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
