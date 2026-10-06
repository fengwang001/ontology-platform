# maintenance — 物业维修工单优先级与承包商派单

一个并发安全、可重放的物业维修派单域服务（无外部依赖，标准库实现）。

## 构造

`New(Config)`，`Config` 给出四级各自的响应/完成时限与升级阈值 `R`：

```go
svc := maintenance.New(maintenance.Config{
    ResponseLimit: [4]int{10, 8, 5, 2},   // 等级1..4
    CompleteLimit: [4]int{30, 20, 10, 5},
    RejectUpgrade: 3,                     // 每累计 3 次拒单升一级
})
```

## 操作（全部携带整数 `now`，单调不回退）

- `RegisterContractor(now, trades, buildings, capacity, acceptsEmergency) int`
- `SubmitOrder(now, tenant, trade, building, level) int`
- `DispatchNext(now) (orderID, contractorID, error)`：按队列优先级派一单；
  队首无候选则跳过，无可派单时返回 `ErrNoCandidate`；紧急单可抢占。
- `Confirm / Reject(now, orderID, contractorID)`
- `Complete(now, contractorID, orderID)`
- `Cancel(now, tenant, orderID)`：仅提交者、且未确认可撤。
- `Deactivate(now, contractorID)`：未确认在手单全部回队。
- `Tick(now)`：只推进时钟并结算逾期，不派单。
- `GetOrder(now, orderID) OrderView`：反映 `now` 下应有状态，不改变系统。
- `Events() []Event`：确定性事件流（派单/拒单/抢占/升级/逾期/停用回队）。

## 关键规则

- 队列：等级高 → 提交早 → 序号小。
- 候选：工种 ∩ 楼栋 ∩ 未满手；紧急单仅派给接受紧急者；拒过此单者跳过。
- 候选次序：在手数少 → 从未完成优先/最近完成更早 → 登记序号小。
- 抢占：满手承包商的未确认非紧急单中，派单最晚者被退回，保留原等级与提交时刻。
- 时限：严格 `now > due` 才算超时；恰等允许确认/不算逾期。
- 升级：第 R、2R… 次拒单触发，紧急封顶；升级后时限从再次派单时刻按新等级重算。

## 错误（按此固定次序只返回第一个）

`ErrInvalidArgument` → `ErrClockBackward` → `ErrNotFound` →
`ErrInvalidState` → `ErrNoCandidate` → `ErrForbidden`。
被拒绝的操作不改变任何工单、承包商与时钟。

## 测试

```bash
go test -race -v ./maintenance/                       # 全量，-v 打印随机对照逐步日志
go test -run TestDifferentialRandom ./maintenance/    # 40 种子×180 步 vs 朴素模型
go test -run BenchmarkSelectContractor -bench . ./maintenance/
```

详见 `DESIGN.md`（关键取舍、被放弃方案、复杂度证明与本地验证）。
