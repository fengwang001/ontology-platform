# 使用说明

## API 速览（包 `ontology`）

- `NewEngine() *Engine`
- `CreateMeter(Meter{ID, Digits, Multiplier, MaxUsagePerUnitTime}) error`
- `Attach(pointID, meterID string, at int64, initial uint64) error`
- `ReplaceMeter(pointID string, at int64, oldLast uint64, newMeterID string, newInitial uint64) error`
- `RegisterReading(meterID string, at int64, value uint64, kind ReadingKind) error`，`kind` 为 `Actual` 或 `Estimated`
- `DeleteEstimatedReading(meterID string, at int64) error`
- `QueryUsage(pointID string, from, to int64) (QueryResult, error)`，返回 `Usage *big.Int` 与 `ContainsEstimated bool`

所有错误均为 `*MeterError`，用 `errors.As` 取出 `Code`：

```go
var me *ontology.MeterError
if errors.As(err, &me) {
    switch me.Code {
    case ontology.ErrUnreasonable: // …
    }
}
```

错误类别（同时也是固定拒绝次序）：
`ErrInvalidArgument`（参数非法）> `ErrNotAttached`（不在挂接期内）>
`ErrReadingScheduleConflict`（与已有读数冲突）> `ErrReadingConflict`（读数冲突）>
`ErrUnreasonable`（不合理）> `ErrNoReading`（无读数）。

## 规则要点

- 显示位数 `Digits` 取 1..18，合法显示值为 `[0, 10^Digits-1]`；倍率 `Multiplier` 必须为正。
- 相邻读数后值小于前值视为恰好翻转一圈：增量 = 模 + 后值 − 前值。
- 合理性：倍率 × 表显增量 ≤ 单位时间上限 × 区间时长，取等通过（big.Int 精确比较）。
- 换表时刻同时是旧表末次、新表初始的实抄读数；早于旧表最新读数或新表任何记录均拒绝。
- 同一电表拆除后允许重新挂接；相邻读数只在当前挂接期 [安装, 拆除]（含两端衔接点）内取。
- 查询端点必须是该供电点某只挂接表上的已有读数时刻；跨表结果按各表倍率分别加权求和。

## 验证

```bash
GOCACHE=/tmp/gocache go test -race -v ./...
```

随机对照测试 `TestNaiveDifferential`（`-short` 可跳过）把同一条操作序列
喂给生产引擎与独立朴素模型，逐条比对错误类别、查询用电量与估算标记，
并对任意三点验证可加性；日志写入 `/tmp/ontology_differential.log`。
