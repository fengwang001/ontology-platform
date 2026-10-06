# API 与语义速查

所有方法都以单调的 `now`（0 ~ 10^9 秒整数）为第一参数；标识必须为非空字符串，
数值参数为非负整数（间隔/窗口/上限为正整数）。失败返回带码错误，且不改变任何状态与时钟。

```go
type Spec struct {
    ID, Patient, Drug, Kind string // Kind: "interval" | "daily" | "prn"
    FirstTime  int64                // interval：首次计划时刻（>= 开立时刻）
    H          int64                // interval：间隔
    TimesOfDay []int64              // daily：日内秒数 0..86399
    PRNMinGap  int64                // prn：同医嘱最小间隔
    PRNMax24   int64                // prn：滚动 24h 次数上限
}
```

| 方法 | 语义 |
| --- | --- |
| `NewSystem(W)` | W 为按时窗口半宽（正整数） |
| `RegisterDrug(now, name, category, minInterval)` | 登记药品类别与最小安全间隔 |
| `SetAllergy(now, patient, item, active)` | item 可药品名或类别；不追溯已开医嘱 |
| `CreateOrder(now, Spec)` | 开立；药品/类别命中过敏整张拒绝 |
| `StopOrder(now, id)` | 停嘱生效；漏给/作废按 `p+W` 与 `now` 严格分界 |
| `ReplaceOrder(now, oldID, Spec)` | 改嘱 = 停旧 + 开新，原子 |
| `Administer(now, id)` | 窗口内按时给药；过跨医嘱安全间隔 |
| `Refuse(now, id)` | 拒服，计处理、不计给药、不影响间隔 |
| `MakeUp(now, id)` | 漏给补给；固定间隔触发分段重排 |
| `AdministerPRN(now, id)` | 必要时给药（间隔 + 滚动次数 + 安全间隔） |
| `QueryPatient(now, patient, lo, hi)` | 闭区间计划点，状态按 `now` 即时判定 |
| `QueryPRN(now, patient)` | PRN 实际给药记录 |

## 状态

`PENDING` 待给 / `GIVEN_ON_TIME` 已给按时 / `MADE_UP` 已补给 /
`REFUSED` 拒服 / `MISSED` 漏给 / `VOID` 作废。

## 边界（恰取等）

- 给药窗口：`p-W <= now <= p+W`。
- 漏给：`now > p+W`；`now == p+W` 仍待给。
- 停嘱分界：`p+W < stop` 漏给；`p+W == stop` 作废。
- 补给截止：`now < next-W`；`now == next-W` 拒绝（报无对应计划点）。
- 安全间隔 / PRN 间隔：差值 `== 最小间隔` 允许。
- PRN 滚动窗口：`(now-86400, now]`，恰在左端点的剂量不计。
- 频次约束：`H > 2W` 且 `H >= 药品最小间隔`；固定时点的每对相邻时点（含跨日首尾）同理。

## 错误处理

```go
var e *medschedule.Error
if errors.As(err, &e) {
    switch e.Code {
    case medschedule.ErrIntervalTooShort: // …
    }
}
```

错误码（优先级从高到低）：
`ErrInvalidParam, ErrClockRollback, ErrNotFound, ErrBadState, ErrAllergy,
ErrNoScheduledPoint, ErrIntervalTooShort, ErrPRNLimit, ErrMakeupNotAllowed`。
