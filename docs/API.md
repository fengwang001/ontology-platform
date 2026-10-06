# API 参考

所有时间为整数分钟，范围 `[0, 10^7]`；标识为非空字符串；时长为正整数。
所有变更操作第一个参数都是 `now`，不得小于上一次被接受操作的 `now`。
被拒绝的操作不改变任何状态（含时钟）。

## 构造

```go
cfg := hemo.Config{
    RegularNegative: 30,  // 阴性后的常规消毒
    RegularHBV:      40,  // 乙肝后的常规消毒
    RegularHCV:      40,  // 丙肝后的常规消毒
    RegularUnknown:  20,  // 待定后的常规消毒
    DeepDisinfect:   120, // 乙肝/丙肝相邻的深度消毒
    MinRecovery:     60,  // 同一患者两次治疗最小间隔（结束->开始）
}
s := hemo.NewSystem(cfg)
```

## 登记

```go
// 机位：zone 为 hemo.ZoneNormal / hemo.ZoneIsolation；
// observation 仅在普通区有效，标记观察位。
err := s.RegisterChair(now, chairID, zone, observation)

err := s.RegisterPatient(now, patientID, hemo.InfectionNegative)
// 感染取值：InfectionUnknown（待定）/ InfectionNegative /
// InfectionHBV（乙肝）/ InfectionHCV（丙肝）
```

## 周期性方案

```go
var wk [7]bool
wk[1] = true; wk[3] = true // 周一、周三；周锚点为分钟 0
plan, err := s.AddPlan(now, patientID, wk,
    dayStart,  // 日内开始时刻，0..1439
    duration,  // 治疗时长（正整数）
    validFrom, // 有效区间起（含）
    validTo,   // 有效区间止（含；治疗不得越过它）
)
// plan.TreatmentIDs 按开始升序给出全部出现次数；分配全有或全无。
//
// 分配规则：存在一台机位能承载全部次数时取编号最小者；否则各次数按开始
// 升序逐次取编号最小可行机位。
```

## 故障与恢复

```go
// 故障窗口 [at, until)：开始时刻落在窗口内的治疗全部改派；进行中的治疗
// （Start < at < End）不受影响。任一改派失败则整次登记被拒绝。
err := s.FaultChair(now, at, until, chairID)

// 恢复：把包含 now 的故障窗口在 now 处截断；不回溯已改派治疗。
err = s.RecoverChair(now, chairID)
```

同一机位允许登记多个互不重叠的未来故障窗口（重叠报状态冲突）。

## 感染状态变更

```go
// 合法迁移：待定 -> 阴/HBV/HCV；阴性 -> HBV/HCV。
// 只重核 Start >= at 的治疗：原机位仍可行则留任，否则改派到编号最小机位；
// 任一改派失败则整次变更被拒绝并恢复原状（含患者感染状态）。
err := s.ChangeInfection(now, at, patientID, hemo.InfectionHBV)
```

## 取消

```go
// 取消单次治疗：必须未开始（Start > now），机位与消毒占用同步释放。
err := s.CancelTreatment(now, treatmentID)

// 取消整个方案：若任一存活出现已开始则整次操作报状态冲突。
err = s.CancelPlan(now, planID)
```

取消不会引起任何其他治疗提前或改派。

## 查询

```go
tv, err := s.GetTreatment(treatmentID) // 单次治疗快照
pv, err := s.GetPlan(planID)           // 方案与其出现 ID
inf, err := s.PatientInfection(patientID)
ids, err := s.ChairTreatments(chairID) // 机位上存活治疗 ID，按开始升序
```

## 错误处理

错误为 `*hemo.OpError`，字段 `Code` 为下列之一（按报告优先级排序）：

```go
hemo.ErrInvalidArgument   // 参数非法
hemo.ErrClockRollback     // 时钟回退
hemo.ErrNotFound          // 对象不存在
hemo.ErrStateConflict     // 状态不符
hemo.ErrIsolationConflict // 感染隔离冲突
hemo.ErrPatientConflict   // 患者自身冲突
hemo.ErrNoFeasibleChair   // 无可行机位
```

```go
var oe *hemo.OpError
if errors.As(err, &oe) {
    switch oe.Code {
    case hemo.ErrClockRollback:
        // ...
    }
}
```

## 并发

所有方法可被多个 goroutine 同时调用，内部串行化，对外等价于某一串行顺序；
相同操作序列重放产生完全相同的机位分配。
