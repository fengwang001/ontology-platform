# toollife —— 数控加工刀具库寿命管理

多加工通道并发向刀组申请刀具，系统按**寿命预占 → 记账 → 破损/换新/锁定/预警**给出确定选刀结果。

- 设计与取舍见 `toollife/DESIGN.md`。
- 生产代码为 `package toollife`；独立朴素参照模型为 `package toollife/naive`（仅供差分测试）。

## 快速上手

```go
svc := toollife.NewService()
cfg := toollife.GroupConfig{
    Basis:        toollife.BasisSeconds, // 或 BasisPieces（整组统一）
    LifeLimit:    600,                   // 单刀寿命上限
    WarnPermille: 800,                   // 预警比例 800‰
    Mode:         toollife.ModeStrict,   // 或 ModeLenient
}
_ = svc.Magazine().AddGroup("mill", cfg)
_ = svc.Magazine().AddTool("mill", "T01")
_ = svc.Magazine().AddTool("mill", "T02")

// 通道申请：返回选中的刀与预占量
res, err := svc.Apply("req-1", "mill", 120)

// 加工结束记账：实际消耗可 <、=、> 预计；预占差额自动释放
_ = svc.Settle("req-1", 130)

// 中止：释放预占、不计消耗
_ = svc.Abort("req-2")

// 破损 / 换新（换新要求无未结算预占）
_ = svc.ReportBroken("mill", "T01")
_ = svc.Replace("mill", "T01", "T01-new")

// 锁定 / 解锁（已耗尽或破损的刀不能解锁为可用）
_ = svc.Lock("mill", "T02")
_ = svc.Unlock("mill", "T02")

// 查询：每刀状态/已用/预占/剩余寿命 + 当前会被选中的刀
snap, _ := svc.Query("mill")

// 已发出的预警
_ = svc.Warnings()
```

错误用 `*toollife.Error` 的 `Code` 区分，优先级为：
`ErrInvalid > ErrGroupNotFound > ErrToolNotFound > ErrConflict > ErrState > ErrRequestNotFound > ErrNoTool / ErrNoCapacity`。

## 测试

```bash
go test ./... -race -count=1
go test ./toollife/ -run TestDifferentialLoggedCase -v   # 随机序列输入/输出/判定依据日志
go test ./toollife/ -run TestSelectionCost -v            # 历史长度两档复杂度对照
```
