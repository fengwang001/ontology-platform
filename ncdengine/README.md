# 车险无赔款优惠等级引擎

`ncdengine` 提供保单年度、续保窗口、有责出险、等级保护、车辆转移、迟报重定级、撤销与保费差额追补能力。

## 快速使用

```go
engine := ncdengine.NewEngine(0)
engine.Register(ncdengine.RegisterInput{
    CustomerID: "customer-1",
    VehicleID:  "vehicle-1",
    StartDay:   0,
    Config: ncdengine.Config{
        RenewalGraceDays: 30,
        AtFaultThreshold: 50,
        ProtectionStart:  3,
        MaxLevel:         6,
        Premiums:         []int{1000, 900, 800, 700, 600, 500, 400},
    },
})
engine.Renew(ncdengine.RenewInput{CustomerID: "customer-1", VehicleID: "vehicle-1", Day: 335})
```

所有时刻均为非负整数天，只允许前进。错误均为包级哨兵错误，例如 `ErrOutsideRenewalWindow`、`ErrAccidentUncovered`，调用方可用 `errors.Is` 区分。

## 验证

```bash
go test ./...
go test -race ./...
go test -v ./ncdengine
go test -bench=BenchmarkRenewalGradingWithLongHistory -benchmem ./ncdengine
```

详细设计见 `ncdengine/DESIGN.md`。
