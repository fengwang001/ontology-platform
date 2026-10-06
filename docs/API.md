# API 说明（`package staffing`）

所有方法都可并发调用。时间参数 `now` 为整数日序号，单调不回退。
错误统一为 `*staffing.Error{Code, Msg, Index}`，用 `staffing.ErrCode(err)`
取类别；批量失败时 `Index` 为最小失败项下标（非批量为 -1）。

## 构造与日志

```go
svc := staffing.New(cooldownDays, graceDays int) *Service
svc.SetLogger(func(staffing.StepLog)) // 可选：逐步输入/输出/判定依据
```

## 管理操作

| 方法 | 说明 |
| --- | --- |
| `AddPosition(now, PositionSpec{ID,BandLow,BandHigh,Headcount})` | 注册岗位（含带宽，两端含） |
| `AddCandidate(now, id)` | 注册候选人 |
| `AddException(now, id, positionID, quarter, total)` | 登记某岗位某季度例外次数 |
| `SetFrozen(now, positionID, frozen)` | 冻结 / 恢复开放 |
| `AdjustHeadcount(now, positionID, total)` | 调整编制；下调低于已占用则拒绝 |

## 通知生命周期

| 方法 | 说明 |
| --- | --- |
| `IssueOffer(now, candidateID, positionID, salary, deadline) (offerID, err)` | 发放，成功即占 1 编制 |
| `IssueBatch(now, positionID, []BatchItem) ([]offerID, err)` | 同岗位批量，全有或全无 |
| `Respond(now, offerID, accept, entryDate)` | 答复；接受时约定入职日，拒绝则释放并冷却 |
| `Onboard(now, offerID)` | 办理入职；`entry ≤ now ≤ entry+grace` |
| `Withdraw(now, offerID)` | 答复前撤回（不冷却） |
| `Cancel(now, offerID)` | 接受后协商取消（释放、不冷却） |
| `Leave(now, candidateID)` | 在岗离职，释放 1 编制 |
| `GetOffer(now, offerID) (Offer, err)` | 查询，触及即惰性结算 |
| `Occupancy(now, positionID) (occupied, onboarded, pending, err)` | 占用查询，整岗触及 |
| `Snapshot(now) (Snapshot, err)` | 纯读取全量状态（不结算、不推进时钟） |

## 错误类别（按优先级，只报第一个）

`INVALID_PARAM` → `CLOCK_ROLLBACK` → `NOT_FOUND` → `INVALID_STATE` →
`FROZEN` → `HEADCOUNT_FULL` → `BAND_EXCEEDED` → `PENDING_EXISTS` →
`COOLDOWN` → `EXPIRED`。

## 最小示例

```go
svc := staffing.New(5 /*cooldown*/, 3 /*grace*/)
_ = svc.AddPosition(0, staffing.PositionSpec{ID: "P1", BandLow: 100, BandHigh: 200, Headcount: 2})
_ = svc.AddCandidate(0, "alice")

id, err := svc.IssueOffer(1, "alice", "P1", 180, 10)   // 带宽内
if err != nil { /* 按 staffing.ErrCode 分支 */ }

_ = svc.Respond(5, id, true, 8)   // 接受，约定 day 8 入职
_ = svc.Onboard(8, id)            // day 8 入职，占用转为在岗
_ = svc.Leave(20, "alice")        // 离职释放
```

## 不变量

`staffing.CheckInvariant(snap)` 校验：

- 每岗位 `occupied == onboarded + pending` 且 `occupied ≤ headcount`；
- 计数非负；
- 任一候选人至多一份未决通知、至多一份在职工龄；
- 内部占用索引与按通知重算结果一致。
