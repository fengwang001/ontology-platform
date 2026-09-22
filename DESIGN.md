# 在途请求 / 乱序应答关联器

三个包，依赖方向单向：`correlate` → `slot`、`reclaim`；`slot` 与
`reclaim` 互不依赖，也不反向依赖。

## slot

单个槽位的状态机：`Pending → Completed | TimedOut | Canceled`，一旦
离开 `Pending` 不可再迁移（恰好一次）。

关联 ID（`slot.ID`，`uint64`）= 高 32 位槽位号 + 低 32 位代号
（generation）。槽位每次 `Begin` 代号加 1，因此复用同一槽位时，上一笔
请求的旧 ID 与新请求的 ID 必然不同。

超时为左闭右开：`now == deadline` 即超时（`ExpiredAt`）。

## reclaim

最小可用 ID 优先：未回收过的槽位按 `0,1,2,…` 顺序发放；一旦有槽位
释放，再分配总是取当前空闲槽位号中的最小值（最小堆保证）。同样的
操作序列必然得到同样的 ID 分配序列。

## correlate

持有互斥锁管理全部状态，注入时钟 `now func() time.Time`；代码内不
出现 `time.Now` / `time.After` / `time.AfterFunc`。

超时为惰性判定：`Request` / `Deliver` / `Cancel` 进入时先按注入时钟
扫描并了结所有到期槽位，无后台定时器。

应答判定（`Deliver`）：

- 槽位从未分配过 → `ErrUnknownID`；
- 代号相同但槽位已了结，且尚未被新请求占用 → `ErrIdleID`；
- 槽位当前被代号更大的新请求占用 → `ErrStaleID`（迟到应答，绝不
  完成新请求）；
- 代号相同且在途 → 恰好完成一次，计数 `Delivered`。

三类失败分别计数（`Counters.Unknown/Idle/Stale`）。容量满时
`Request` 返回 `ErrCapacity`，且发生在任何状态变更之前：在途数、
槽位状态、计数器均不变。取消不存在/已了结请求返回可判定错误且
不改状态。

查询（`Lookup` / `InFlight` / `Counters`）不做惰性超时清理，同一
注入时刻连查两次结果完全相同。其语义后果：一笔已到超时时刻、但
尚未经过任何写操作的请求，在查询中仍显示为 pending；它的超时会在
下一次 `Request` / `Deliver` / `Cancel` 时被惰性确认。已了结或从未
分配的 ID 查询为零值 `Info{}`。

## 运行

```bash
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/demo
```
