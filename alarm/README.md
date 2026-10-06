# Alarm Lifecycle Package

`alarm` 包提供工业控制室报警生命周期管理，支持：

- 报警触发、返回、确认、手动屏蔽、屏蔽解除、停用和启用。
- 震荡报警的滑动窗口自动屏蔽。
- 基于工况信号的条件抑制。
- 操作员可见活动报警列表与报警率查询。
- 可区分错误原因和优先级的拒绝结果。
- 全服务互斥串行化，满足并发调用等价于某个串行顺序。

## 快速示例

```go
service, err := alarm.NewService(alarm.Config{
    HighManualDuration: 600,
    LowManualDuration:  300,
    ChatterWindow:      60,
    ChatterCount:       4,
    ChatterDuration:    180,
}, []alarm.PointConfig{
    {ID: "TI-1001", Priority: alarm.High, Conditions: []string{"startup"}},
})

_, err = service.Trigger(alarm.Operation{
    At:      10,
    PointID: "TI-1001",
    Role:    alarm.Operator,
})

list, err := service.ActiveAlarms(10)
rate, err := service.AlarmRate(70, 60)
```

手动屏蔽可由操作员执行，停用必须由工程师执行；工程师继承操作员权限：

```go
service.Suppress(alarm.Operation{
    At:       20,
    PointID:  "TI-1001",
    Role:     alarm.Operator,
    Duration: 300,
    Reason:   "现场检修",
})

service.Disable(alarm.Operation{
    At:      21,
    PointID: "TI-1001",
    Role:    alarm.Engineer,
    Ticket:  "CHG-2026-001",
})
```

工况信号是系统输入：

```go
service.SetCondition(alarm.ConditionUpdate{
    At:        30,
    Condition: "startup",
    Active:    true,
})
```

默认 JSON 日志写入标准错误，可通过 `WithLogger(io.Writer)` 重定向。日志包含操作名、输入、输出、报警点关键字段以及判定依据。

详细设计、复杂度证明和性能验证见 `DESIGN.md`。
