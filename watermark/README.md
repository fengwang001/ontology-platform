# watermark — 多分区合并水位组件

将多个分区各自上报的水位合并为一个**单调前进**的合并水位，并通过空闲判定
保证长时间无数据的分区不会卡死整体水位。

## 核心概念

- **处理时间（`Time`）**：由调用方显式注入的逻辑时间（可以是毫秒、微秒或单调
  事件序号）。组件不读墙上时钟，要求时间只进不退；相等时间戳是合法的。
- **分区水位（`Watermark`）**：分区上报的水位标量，同样由调用方约定语义，
  合法域为非负整数，且同一分区只进不退。
- **最后活跃时间**：分区每次成功 `Report` 时，把该分区的最后活跃时间刷新为
  本次注入的处理时间。
- **空闲阈值（`IdleThreshold`）**：构造时给定的非负时长。

## 规则

### 1. 空闲判定

设当前注入的处理时间为 `now`，分区最后活跃时间为 `lastActive`，则

```
elapsed = now - lastActive
分区空闲  <=>  该分区曾上报过水位 且  elapsed >= IdleThreshold
```

- 边界取**包含**语义：`elapsed == IdleThreshold` 恰好相等时即判定为空闲。
- 从未上报过水位的分区不视为活跃也不视为空闲，只是“无数据”，不参与候选。
- 空闲分区一旦再次成功 `Report`，`lastActive` 被刷新、`elapsed` 归零，
  立即恢复为活跃分区。

### 2. 候选值

每次注入时间或上报水位后重新计算：

- 候选分区集合 = **曾上报水位且非空闲**的全部分区。
- 候选值 = 候选分区水位的**最小值**（min）。

### 3. 合并水位推进

设旧合并水位为 `merged`：

- 候选集合**非空**：`merged = max(merged, 候选最小值)`。
  即只在最慢的活跃分区超过当前合并水位时才向前推进，保证**只进不退**。
- 候选集合**为空**（全部空闲，或尚无任何分区上报）：合并水位**保持不变**。

空闲分区上报一个低于当前合并水位、但不低于其自身历史水位的值时，分区会恢复
活跃，但合并水位不会被拉低（被 `max(merged, …)` 兜住）。

## 用法

```go
m, err := watermark.New(watermark.Config{
    Partitions:    3,
    IdleThreshold: 10,                 // elapsed >= 10 时空闲
    Logger:        log.New(os.Stderr, "", log.LstdFlags), // 可选
})
if err != nil { /* 参数非法 */ }

wm, err := m.Report(partition, processingTime, watermark) // 上报并重算
wm, err = m.AdvanceClock(processingTime)                  // 只推进时间并重算
cur := m.Merged()                                         // 并发安全读取
snap := m.Snapshot()                                      // 各分区空闲/水位一致快照
```

## 拒绝原因（可区分、操作不留痕）

所有写操作先完整校验，通过后才落状态；被拒绝时分区水位、活跃时间、时钟与
合并水位均不改变。错误均可用 `errors.Is` 判定：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | 构造时分区数 `<= 0`，或空闲阈值 `< 0` |
| `ErrInvalidArgument` | 注入的时间或上报水位为负 |
| `ErrPartitionOutOfRange` | 分区下标不在 `[0, Partitions)` |
| `ErrClockRollback` | 注入时间早于上一次注入的时间 |
| `ErrWatermarkRollback` | 分区上报水位低于该分区此前水位 |

校验顺序固定：参数合法性 → 分区越界 → 时钟回退 → 分区水位回退。

## 并发与确定性

- `Report` / `AdvanceClock` 互斥；`Merged` / `Now` / `Snapshot` 取读锁，
  可与写操作并发并读到一致快照。
- 合并水位单调不减，因此任何并发读者观察到的序列也单调不减。
- 状态更新完全由注入的输入序列决定，不含随机量或墙上时钟；同一输入序列在
  任意实例上反复计算得到完全相同的输出。

## 日志

注入 `Logger`（标准库 `*log.Logger` 即满足接口）后，每次操作都会打印一行：
输入（操作类型、时间、分区、上报值、上一时间）、各分区的
`wm / reported / elapsed / idle`、空闲阈值、活跃分区集合与候选最小值、
合并水位前后值及推进依据。例如阈值为 10：

```
watermark op=report at=0 partition=0 reported=100 prev_clock=unset | partitions=[{p=0 wm=100 reported=true elapsed=0 idle=false} {p=1 wm=n/a reported=false elapsed=n/a idle=false}] threshold=10 | active_partitions=[0] candidate_min=100 | merged 0 -> 100 advanced=true (advanced to minimum watermark of active partitions)
watermark op=advance_clock at=10 prev_clock=5 | partitions=[{p=0 wm=100 reported=true elapsed=10 idle=true} {p=1 wm=300 reported=true elapsed=5 idle=false}] threshold=10 | active_partitions=[1] candidate_min=300 | merged 100 -> 300 advanced=true (advanced to minimum watermark of active partitions)
watermark op=report at=10 partition=0 reported=150 prev_clock=10 | partitions=[{p=0 wm=150 reported=true elapsed=0 idle=false} {p=1 wm=300 reported=true elapsed=5 idle=false}] threshold=10 | active_partitions=[0,1] candidate_min=150 | merged 300 -> 300 advanced=false (minimum active watermark not ahead of merged: held)
```

第二行展示了 p0 在 `elapsed == threshold` 恰好空闲、不再拖住合并水位；
第三行展示了空闲分区恢复时上报低于合并水位的值，合并水位保持 300 不回退。

## 本地验证

```bash
# 单元测试（含空闲边界、恢复低报、全部空闲、各类非法输入、确定性、并发读、日志）
go test ./watermark/

# 竞态检测 + 详细输出（验证并发读单调、无数据竞争）
go test -race -v ./watermark/

# 全量测试与静态检查
go test -race ./...
go vet ./...
gofmt -l .
```
