# taxipool：机场出租车蓄车池虚拟排队

实现按候机楼分区的虚拟排队、短途返回优先凭证、爽约禁入与跨候机楼调剂。
所有改变状态的方法携带单调非递减时刻；并发调用可线性化；相同操作序列
重放得到完全相同的队列历史与凭证记录。

## 操作流程

司机生命周期：`idle → queued → dispatched → serving → idle`（可循环）。

1. `RegisterDriver(driver, at)`：注册司机（幂等）。
2. `Join(driver, terminal, at)`：入池。系统按该司机上一次
   `CompleteTrip` 的离开时刻与距离判定短途返回：距离 `<= ShortTripMeters`
   （取等）且间隔 `<= ReturnLimit`（取等）时发优先凭证；当日发放数按
   配置时区切自然日计数，超过 `DailyVoucherLimit` 不发。
   - 持有效凭证入池：排在该队列所有普通司机之前、已有优先司机之后；
   - 凭证 `VoucherTTL` 后失效，`at == 过期时刻` 视为已过期；
   - 队列优先人数已达 `PrioritySlots`：本次按普通入队，凭证第一次保留、
     第二次仍受限即作废；
   - 满员/禁入被拒：不占座、不消耗凭证，凭证 TTL 自发放时刻继续计算。
3. `Dispatch(terminal, at)`：本队列取队首；本队列空时在其余队列中选
   队首（必须是普通司机）入池时刻最早者，并列取候机楼标识字典序最小者；
   本候机楼放行 deadline = `at + ArrivalLimit`，调剂 = `at + TransferLimit`。
4. `Arrive(driver, at)`：`at < deadline` 成功；`at == deadline` 即逾期，
   记一次爽约；累计达 `NoShowLimit` 自 deadline 起禁入 `BanDuration`，
   `at == banUntil` 时禁入恰好结束、可再次入池。
5. `CompleteTrip(driver, at, distanceMeters)`：载客完成，记录离开时刻与距离。
6. `Leave(driver, at)`：仅 queued 可主动离队；dispatched 期间返回
   “放行后到达之前不可离队”。离队不消耗凭证、不计爽约。
7. `Position(driver)`：返回前方人数，只读、不推进时钟；`LastPositionScan`
   暴露查询遍历的节点数以验证开销只与当前队长有关。
8. `Snapshot(terminal)` / `Inspect(driver)`：队列与司机状态的只读快照。

## 错误类别

`errors.Is(err, taxipool.ErrXxx)` 或 `taxipool.KindOf(err)` 判定，顺序固定：
参数非法、时钟回退、候机楼不存在、司机不存在、司机禁入中、司机已在队列中、
队列已满、无车可放行、司机未处于放行中、到达已逾期；补充两类：
放行后到达之前不可离队、司机不在队列中。被拒绝操作不改变任何状态与时钟。

## 最小示例

```go
pool, _ := taxipool.New(&taxipool.Config{
    Terminals: map[string]int{"T1": 20},
    ArrivalLimit: 10 * time.Minute, TransferLimit: 5 * time.Minute,
    ShortTripMeters: 3000, ReturnLimit: 30 * time.Minute,
    VoucherTTL: time.Hour, DailyVoucherLimit: 2, PrioritySlots: 3,
    NoShowLimit: 2, BanDuration: 2 * time.Hour,
    TimeZone: time.FixedZone("CST", 8*3600),
})
_ = pool.RegisterDriver("A", t0)
r, err := pool.Join("A", "T1", t0)        // 入池，r.Position 为前方人数
d, err := pool.Dispatch("T1", t0.Add(time.Minute))
_ = pool.Arrive("A", t0.Add(2*time.Minute))
_ = pool.CompleteTrip("A", t0.Add(20*time.Minute), 2500) // 短途
r2, _ := pool.Join("A", "T1", t0.Add(40*time.Minute))    // 持凭证优先入队
```

## 验证

```bash
go test ./taxipool -v                                              # 边界用例
go test ./taxipool -run TestRandomDifferential -v                 # 朴素模型随机对照+日志
go test -race ./...                                                # 竞态
```
