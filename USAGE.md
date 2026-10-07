# 楼宇总表/分户表抄表记账服务

包路径：根包 `ontology`（`package billing`）。入口为 `Building`。

## 快速上手

```go
b := billing.NewBuilding(2) // G=2：距上次实抄严格超过 2 个时刻单位才可估抄
b.AddMeter("M", 1000, true, 0)          // 总表，量程 1000
b.AddMeter("m1", 1000, false, 0)        // 分户表
b.AddHousehold("h1", "m1", 90, 0)       // 面积 90
b.AddOccupancy("h1", 0, 30, 1)          // 在住 [0,30)

b.EnterReading(billing.ReadingInput{MeterID: "M", Time: 0, Value: 0, Now: 1})
b.EnterReading(billing.ReadingInput{MeterID: "m1", Time: 0, Value: 0, Now: 1})
b.EnterReading(billing.ReadingInput{MeterID: "m1", Time: 30, Value: 120, Estimated: true, Now: 2})
s, err := b.Settle(0, 30, 3)            // s.Self / s.Shared / s.Master
b.Corrections()                         // 后续回补/替代产生的更正链
```

## API

- `NewBuilding(G)`：构造服务。
- `AddMeter(id, cap, master, now)`、`AddHousehold(id, meterID, area, now)`、
  `AddOccupancy(houseID, start, end, now)`。
- `EnterReading(ReadingInput{...})`：实抄 `Estimated:false`、估抄 `true`；
  更晚实抄自动替代估抄并从序列移除，被替代估抄多估部分通过更正退回。
- `ChangeMeter(meterID, now, oldFinal, newStart, newCap)`：换表；当刻不计用量。
- `Settle(start, end, now)`：账期必须首尾相接，不可重复结算。
  返回 `Settlement{Self, Shared, Master}`。
- `Corrections()`：每个已结算账期的应付差额（不改动账单本身）。

## 错误码（固定次序，只报告第一个）

`ErrInvalidArgument` → `ErrClockRollback` → `ErrMeterNotFound` →
`ErrReadingOutOfOrder` → `ErrReadingIllegal` → `ErrEstimateNotAllowed` →
`ErrPeriodAlreadySettled` → `ErrSharedNegative`。

## 语义要点

- 翻转：后值<前值且差额 ≤ cap/2 视为翻转一次；差额 > cap/2 拒绝。
- 线性归属：读数时刻不与账期边界重合时，非末端账期取 `floor` 级联差；
  末端点落在账期内部时该账期取全部余量；边界重合时余量归下一账期。
- 公摊：`总表用量 - 分户之和`，为负则拒绝结算且不留痕；
  按 `面积×在住时长` 精确整数分摊，空置户仍参与，和恒等于公摊。
- 并发：所有操作互斥串行，等价某合法串行顺序；同时刻读数至多一条。

## 测试与验证

```bash
go test ./...                                   # 场景 + 随机朴素对照
go test -race -count=2 ./...                    # 竞态
go test -run TestRandomAgainstNaive -v -billing-log   # 逐步输入/输出/判定日志
go test -bench BenchmarkSettleScaling -benchtime=2000x ./...  # 复杂度证据
```
