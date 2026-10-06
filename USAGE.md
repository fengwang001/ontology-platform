# 机票改签差价与退票阶梯结算系统

金额单位：分（非负整数 `int64`）；时刻单位：秒（非负整数）。

## 配置

```go
sys := ontology.New(ontology.Config{
    LongThreshold:  200,           // >= 长阈值：最宽松档
    ShortThreshold: 30,            // [短,长)：中档；(0,短)：最严档
    RefundPercents: [3]int{5,20,40},  // 退票三档比例（远/中/近）
    ChangePercents: [3]int{3,12,30},  // 改签三档比例（独立于退票）
    MaxChanges:     3,             // 自愿改签次数上限（非自愿不计）
    VoucherTTL:     500,           // 代金券有效期（秒）
})
```

## API（均并发安全）

- `RegisterFlight(id, fare, departure)`：注册航班（目标航班也需先注册）。
- `PurchaseTicket(id, owner, flightID)`：出票。
- `CancelFlight(flightID, now) (n, err)`：航司取消，当前在该航班上未退的票标记非自愿。
- `Refund(ticketID, now) (RefundResult, error)`：退票。
  - 自愿：退 `当前票面 - 退票手续费`；历次改签费不退。
  - 非自愿：退 `当前票面 + 历次已付改签费`（代金券不退）。
- `Change(ChangeRequest{TicketID,TargetID,VoucherID,Cash}, now) (ChangeResult, error)`：
  - 改签费按“原航班出发时刻”计档；差价 = 目标票面 - 当前票面。
  - 正差价现金补付；负差价不退现金、等额生成归属购票人的代金券。
  - 可用一张同主、未过期（恰到期算过期）、未用尽的券抵扣，余额留原券。
  - 现金必须恰等于应补现金，否则错误 `KindPaymentMismatch` 中带 `CashDue`。
  - 非自愿改签免费、正差价免补、负差价不生券、不计次，改后清非自愿标记。
- `QuoteRefund` / `QuoteChange`：纯查询报价，O(1)，不推进时钟。

## 错误类别（返回 `*OpError`，`Kind` 越小数优先级越高）

`KindInvalidArgument` > `KindClockRewind` > `KindTicketNotFound` > `KindTicketState` >
`KindDeparted` > `KindChangeLimit` > `KindVoucherNotFound` > `KindVoucherOwner` >
`KindVoucherExpired` > `KindVoucherUsed` > `KindPaymentMismatch`。

## 验证

```bash
export GOCACHE=/tmp/gocache   # 若默认缓存目录只读
go test -race ./...
go test -run TestNaiveDiffRandom -v .   # 随机差分（含逐步日志与现金守恒）
go test -run TestQuoteO1 -v .           # O(1) 证据
go test -bench BenchmarkQuoteChangeConstant -benchtime=100x .
```
