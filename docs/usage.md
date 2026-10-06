# 使用文档：`teaching` 包

## 1. 初始化

```go
cfg := teaching.Config{
    Tiers: []teaching.ScaleTier{
        {MaxSize: 30, Coeff: 100},   // 1–30 人
        {MaxSize: 60, Coeff: 120},   // 31–60 人（60 归高档）
        {MaxSize: 120, Coeff: 150},  // 61–120 人
    },
    NewCourseAdd: 10,   // 新开课 110%
    LabCoeff:     110,  // 实验课 110%
    Ranks: map[string]teaching.Rank{
        "prof": {Name: "prof", MinLoad: 100, MaxLoad: 200},
    },
    ConfirmTicks: 10,   // 待确认有效期（逻辑时钟单位）
}
svc, err := teaching.NewService(cfg, 0 /*初始时钟*/)
```

## 2. API 一览

所有操作都接受当前逻辑时钟 `now`（单调不减）。返回 `*teaching.Error`，
其 `Code` 为下列之一，`Index` 仅批量指派有意义：

| Code | 常量 | 含义 |
| --- | --- | --- |
| 1 | `ErrInvalidParameter` | 参数非法 |
| 2 | `ErrClockRollback` | 时钟回退 |
| 3 | `ErrNotFound` | 教师或任务不存在 |
| 4 | `ErrIllegalState` | 状态不允许（重复指派、冻结、非 pending 等） |
| 5 | `ErrSlotConflict` | 周×节次时段冲突 |
| 6 | `ErrOverCap` | 超职级上限 |
| 7 | `ErrHoursNotConserved` | 合上学时之和 ≠ 课程学时 |

- `AddTeacher(id, rank, now)`
- `AddTask(TaskSpec, now)`：学时必须 ≥ 周数（每周至少 1 学时）。
- `Assign([]AssignmentReq, now)`：**全有或全无**批量指派，
  成功后全部为 `pending`，立即占用时段与上限。
- `Respond(teacher, task, accept, now)`：确认/拒绝。
  `now == deadline` 仍有效；已超时释放返回 `ErrIllegalState`。
- `Replace(fromTeacher, task, toTeacher, fromWeek, now)`：
  `fromWeek ∈ (WeekStart, WeekEnd]`，立即生效；失败不动任何状态。
- `Settle(teacher, semester, now)`：幂等核算，返回 `SettlementResult`，
  核算后该 `(semester, teacher)` 冻结。
- 查询（也会推进时钟并触发惰性释放）：`Touch`、`Now`、`ActiveLoad`、
  `ListAssignments`、`Occupied(teacher, week, period, now)`、`TaskAllocated`。

## 3. 典型流程

```go
_ = svc.AddTeacher("a", "prof", 1)
_ = svc.AddTask(teaching.TaskSpec{
    ID: "c1", Semester: "2024-1",
    Hours: 20, ClassSize: 60, NewCourse: true,
    WeekStart: 1, WeekEnd: 10, Periods: []int{1, 3},
}, 2)

// 两人合上，学时之和必须恰为 20
err := svc.Assign([]teaching.AssignmentReq{
    {TeacherID: "a", TaskID: "c1", Hours: 12},
    {TeacherID: "b", TaskID: "c1", Hours: 8},
}, 3)

_ = svc.Respond("a", "c1", true, 4)
_ = svc.Respond("b", "c1", true, 5)

// 第 4 周起把 a 的剩余部分交给 c（零头学时按规则归首任 a）
_ = svc.Replace("a", "c1", "c", 4, 6)

res, err := svc.Settle("a", "2024-1", 200)
// res: Load / Shortfall / Excess / CreditEarned / CreditUsed / Unmet
```

## 4. 错误处理

```go
if e, ok := err.(*teaching.Error); ok {
    switch e.Code {
    case teaching.ErrSlotConflict: // 调整节次/周次
    case teaching.ErrHoursNotConserved: // 重新切分合上学时
    case teaching.ErrOverCap: // 减少指派或换人
    }
    // 批量指派：e.Index 是下标最小的失败项
}
```

## 5. 不变量速查

- 每任务活跃学时之和恒等于其总学时（pending+confirmed；换人后仍成立）。
- 教师在任一 `(week, period)` 格至多被一个**不同**任务占用。
- 教师活跃折算工作量 ≤ 职级上限（含 pending）。
- `Settle` 对同一 `(semester, teacher)` 重复调用结果完全相同。
- 被拒绝操作（含批量中任一项失败）不改变任何状态，也不推进时钟。
