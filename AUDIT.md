# 审计：不变量、自检与复杂度

## 不变量逐条核对
1. **不早触发、不漏触发、只触发一次**
   - 保证：触发判定 `now >= deadline`（`cascade.Due`，`scheduler/advance.go` 的 `step`）；落槽数学保证期限为 d 的元素恰在 `now` 到达 d 时位于层 0 游标槽（`cascade.Place` + `wheel.AdvanceTo`）；`timer.Fire()` 的 `Pending→Fired` 一次性迁移保证只触发一次。
   - 测试：`TestFireTiming`（表驱动：delay 0/1/5/64/100，分段与越界推进，恰达边界触发且不重复）。
2. **`Advance(100)` ≡ 100×`Advance(1)`**
   - 保证：`Advance` 内部逐 tick 调用 `step`；降级是 `now` 的纯函数（`now ≡ 0 mod S^L` 时惰性降级），与推进的切分方式无关。
   - 测试：`TestAdvanceEquivalence`（4 种切分逐位比对触发序列）。
3. **同 tick 顺序确定（= Add 先后，与层级槽位无关）**
   - 保证：每个到期批触发前按单调 `seq` 排序（`step` 末尾 `sort.Slice`），`seq` 即 Add 顺序。
   - 测试：`TestSameTickOrder`（含「同 deadline 落在不同层」与「Add 序优先于到达序」）。
4. **取消即不触发（含已取出待触发的窗口）**
   - 保证：`fireBatch` 逐条在锁内复查 `state==Pending && gen 未变` 才置 `Fired` 并回调；`Cancel` 置 `Cancelled`、`Reset` 递增 `gen`，落在此窗口内均被跳过。
   - 测试：`TestCancelInFlight`（回调内取消/重置兄弟定时器）；`TestConcurrentAddCancelAdvance`（被取消者零触发）。
5. **句柄幂等且结果互异**
   - 保证：状态机 + 墓碑保留在 `timers` 表；`Cancel`/`Reset` 按状态返回 `ErrTimerCancelled`/`ErrTimerFired`/`ErrTimerNotFound`（`scheduler/errors.go`）。
   - 测试：`TestHandleIdempotency`（5 种场景表驱动，且校验无关定时器不受影响）。

## 自检
`Scheduler.SelfCheck`（`scheduler/advance.go`）：各层 `currentTime == now` 对齐值且游标换算一致；轮内元素总数 + 有效 pending 数 == Pending 状态总数 == `live`；槽内无 `Fired`/`Cancelled` 元素；元素登记位置与实际所在槽一致。
测试：`TestSelfCheck`（5 种状态表驱动）、`TestConcurrentAddCancelAdvance` 末尾。

## 故障注入与上限
- 参数错误（负延迟、`Advance(0)`/负数）拒绝且不改状态：`TestParamErrors`。
- `MaxAdvance`/`MaxTimers`/`MaxDelay` 超限立即拒绝、无半登记、可继续工作：`TestLimits`。

## 复杂度（第四节）
- 机制：`Advance(1)` 只触碰层 0 的一个槽（无回绕时），与高层存量 N 无关；计数器为非导出字段 `touchedSlots`/`touchedTimers`。
- 实测（`TestTouchedIndependentOfN`，延迟均为 1 000 000 的远未来定时器，`Advance(1)`）：
  - N=1 000：slots=1，timers=0
  - N=100 000：slots=1，timers=0
  - 增长为 0，远小于 100 倍。
