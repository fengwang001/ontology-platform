# Battery Protection Manager

Go 实现的电池包保护、电流降额和人工复位锁存故障管理器。

## 数据模型

- 时间：非负整数毫秒，接受采样时间必须严格递增。
- 电压：毫伏，全部单体电压必填，合法域为 `0..5000`。
- 温度：`TemperatureReading{Valid, ValueDeciC}`，可逐点无效。
- 电流：毫安，正数为充电、负数为放电；合法域排除最小 `int64`。
- 限流表：`LimitTable{Boundaries, Permilles}`，档位左闭右开，最后一档向右开放。

## 快速使用

```go
cfg := battery.Config{
    CellCount:    3,
    TemperatureCount: 2,
    ChargeVoltageTable: battery.LimitTable{
        Boundaries: []int64{0, 3000, 4000},
        Permilles:  []int{200, 500, 1000},
    },
    // DischargeVoltageTable、ChargeTemperatureTable、DischargeTemperatureTable 同理。
    RatedChargeCurrentMA:    1000,
    RatedDischargeCurrentMA: 2000,
    OvervoltageThresholdMV:  4000,
    UndervoltageThresholdMV: 3000,
    DeltaVoltageLimitMV:     50,
    RecoveryHysteresisMV:    10,
    ConfirmationDurationMS:  10,
    IdleCurrentThresholdMA:  100,
    OvercurrentTiers: [3]battery.OvercurrentTier{
        {ExcessMA: 1, ToleranceMS: 20},
        {ExcessMA: 101, ToleranceMS: 10},
        {ExcessMA: 201, ToleranceMS: 0},
    },
}

manager, err := battery.NewManager(cfg)
snapshot, err := manager.Submit(battery.Sample{
    TimeMS:      1,
    VoltagesMV:  []int64{3500, 3500, 3500},
    Temperatures: []battery.TemperatureReading{
        {Valid: true, ValueDeciC: 250},
        {Valid: true, ValueDeciC: 255},
    },
    CurrentMA: 120,
})

_ = snapshot.ChargeCurrentLimitMA
_ = snapshot.DischargeCurrentLimitMA

if err := manager.Reset(true); err != nil {
    // 按 Error.Kind 区分无权限、未锁存、无新采样、恢复条件不满足。
}
```

## 快照字段

- `ChargeCurrentLimitMA`、`DischargeCurrentLimitMA`：当前方向允许电流，非负。
- `ChargeProhibited`、`DischargeProhibited`：过压/欠压保护状态。
- `BalancingRequested`：最高最低单体压差超限的均衡请求。
- `SensorFault`：最近采样所有温度点均无效。
- `Latched`、`LatchReasons`：人工复位锁存状态及全部已触发原因。

## 错误区分

`*battery.Error` 的 `Kind` 可取：

- `invalid_config`
- `invalid_sample`
- `time_not_advancing`
- `unauthorized`
- `not_latched`
- `no_sample_after_latch`
- `recovery_not_met`

采样拒绝顺序固定为：数量不符、数值越界、时间不严格递增。

## 验证

如 Go 工具链不在 `PATH`，可显式使用 `/usr/local/go/bin/go`，并用 `/tmp` 作为构建缓存：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go vet ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -v -run TestRandomNaiveModelComparison ./...
```

更多设计取舍见 [DESIGN.md](DESIGN.md)。
