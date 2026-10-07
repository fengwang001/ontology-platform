# ncd — 车险无赔款优惠等级续保定级引擎

## 快速开始

```go
eng, err := ncd.NewEngine(ncd.Config{
    MaxLevel:          5,                          // 等级 0..5
    RenewalGraceDays:  10,                         // 到期日后 10 天内可连续续保
    LiableThreshold:   50,                         // 责任比例 >= 50 即有责
    ProtectStartLevel: 3,                          // 等级 >= 3 可购保护
    Premiums:          []int64{1000, 900, 800, 700, 600, 500},
})
```

## 操作一览

| 方法 | 语义 |
| --- | --- |
| `Insure(被保人, 保单号, 日)` | 首次投保/中断后重保，等级 0；在保则报「已有在保保单」 |
| `Renew(被保人, 日)` | 窗口 `[到期日-30, 到期日+N]` 内连续续保并返回新等级；早于左端报「续保窗口外」，晚于右端中断清零 |
| `ReportClaim(被保人, 事故号, 事故日, 责任比例, 日)` | 登记出险；事故日落历史年度时追溯重定级并产生追补 |
| `WithdrawClaim(被保人, 事故号, 日)` | 撤销出险并重定级，等级轨迹与从未登记一致 |
| `BuyProtection(被保人, 日)` | 购买日所在年度购一次保护，抵第一次有责的降级 |
| `Transfer(被保人, 新保单号, 日)` | 原保单终止，新保单继承等级与当前年度出险 |
| `Level` / `Years` / `Surcharges` / `Policies` / `Now` | 只读查询 |

## 约定
- 时刻为整数天，全局只能前进；所有入口并发安全（内部串行化）。
- 错误为 `*ncd.Error`，用 `Code` 区分十类拒绝原因；被拒操作不改变任何状态。
- 设计取舍见 [DESIGN.md](./DESIGN.md)。

## 验证

```bash
go test ./ncd/            # 全部单测与随机对照
go test -race ./ncd/      # 并发与竞态
go test -v -run TestDifferentialRandom ./ncd/  # 查看对照日志
```
