# archive — 机关档案借阅与预约服务

纯 Go、零外部依赖的内存服务。所有时间相关方法接收整数日 `now`，并保证：

- `now` 单调；回退报 `ErrClockRollback`，且被拒操作零副作用。
- 密级资格、借期/续借窗口、取卷期限、逾期与暂停阈值、冷静期均为**闭区间整数边界**（恰等成立）。
- 预约严格先到先得；队首资格丧失时跳过但保留位置。
- 多卷同借全有或全无，报错误优先级最高的失败项（同优先级取最小下标）。
- 所有方法可并发调用（内部互斥），结果等价于某串行顺序；相同序列重放逐字节一致。

## API 速览

```go
svc := archive.NewService(archive.Config{
    LoanDays:           [4]int{10, 10, 10, 10}, // 公开 内部 机密 绝密
    PickupDeadlineDays: 3,
    RenewWindowDays:    5,
    MaxRenewals:        1,
    OverdueThreshold:   5,
    CooldownDays:       3,
})
svc.AddUser("u0", archive.ClassTopSecret)
svc.AddVolume("v0", archive.ClassConfidential)

svc.Borrow(0, "u0", "v0")                 // 借
svc.Reserve(1, "u1", "v0")                // 预约借出中的卷
svc.Return(10, "u0", "v0")                // 归还（自动分配下一位）
svc.Pickup(11, "u1", "v0")                // 取走分配给本人的卷
svc.Renew(6, "u0", "v0", "boss")          // 续借（机密以上需非本人 approver）
svc.BorrowBatch(1, "u1", []string{"v1", "v2"}) // 多卷同借
svc.CancelReservation(2, "u1", "v0")      // 取消预约/放弃待取
svc.Seal("v0")                            // 封存
svc.SetClassification("u1", archive.ClassInternal)
svc.Advance(20)                           // 仅推进时钟（到期放弃/暂停解除）

svc.Snapshot()                            // 确定性完整状态文本
svc.GetVolume("v0")                       // 单卷只读快照
svc.ScanSteps("v0")                       // 最近一次分配扫描步数（开销可验证）
```

## 错误类别（按优先级，数值小者先报）

`ErrInvalidParam` → `ErrClockRollback` → `ErrNotFound` → `ErrState` →
`ErrClearance` → `ErrSuspended` → `ErrAlreadyLent` →
`ErrRenewNotAllowed` → `ErrReservation`。

每个 `Outcome` 含 `OK / Err / Reason`；`Reason` 给出本次判定依据（窗口实际区间、下标等）。

## 参考模型与测试

- `naive` 包是独立编写的朴素模型：切片队列、每步全局结算、全量扫描，与实现**不共享任何业务逻辑代码**。
- 随机测试（默认 40 个种子 × 400 步）逐步比对返回错误类别、批量失败下标与完整快照。
- 逐步日志：边界测试写到 `testdata/trace.*.log`；随机对照用环境变量开启：
  `RANDOM_TRACE_LOG=路径 go test ./archive/ -run TestRandomDifferential`。

更多取舍与被放弃方案见 `docs/DESIGN.md`。
