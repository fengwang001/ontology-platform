# settlement — 证券交割日终批处理

日终批处理与交割失败处理库：券款对付、部分交割、失败滚动、
按日罚金与到期强制了结。详细设计见 [DESIGN.md](./DESIGN.md)。

## API 速览

```go
sys, _ := settlement.NewSystem(settlement.Config{
    BusinessDays: []int64{1, 2, 3}, // 营业日（整数天，不必连续）
    MaxFailDays:  3,                 // B：累计失败营业日数阈值
    PenaltyBPS:   100,               // 罚率基点
})
_ = sys.AddAccount("buyer", nil, 1000)
_ = sys.AddAccount("seller", map[int64]int64{10: 5}, 0)

err := sys.RegisterOrder(settlement.Order{
    ID: 1, Security: 10, Buyer: "buyer", Seller: "seller",
    Qty: 5, Price: 10, SettleDay: 1, AllowPartial: false,
})

err = sys.RunBatch(1, map[int64]int64{10: 10}) // 当日各证券参考价

acct, _ := sys.QueryAccount("buyer")  // 头寸与应付应收
ord, _ := sys.QueryOrder(1)           // 状态/已交割量/累计失败日数
rep, _ := sys.LastReport()            // 当批每指令判定依据（可复现日志）
```

## 状态与错误

- 指令状态：`StatusPending` / `StatusPartial` / `StatusComplete` / `StatusForceClosed`。
- 哨兵错误：`ErrInvalidParam`、`ErrNonBusinessDay`、`ErrOrdering`、
  `ErrDuplicateID`、`ErrAccountMissing`、`ErrDatePassed`、`ErrOrderNotFound`，
  用 `errors.Is` 判别。

## 测试

```bash
go test -v ./settlement          # 场景测试 + 2000 条随机差分序列
go test -race ./settlement/...   # 并发竞态
go test -run=NONE -bench=BenchmarkDayCost ./settlement
```
