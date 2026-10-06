# bus — 公交线路车辆排班与串车调整

单线路、可精确重放的车辆排班服务。覆盖基于发车间隔的运行控制、串车识别与
三种干预（控制站扣车、跳站快车、备车插入），并强制司机连续在岗工时约束。

## 核心概念

- 站点：顺序排列的 0 基站点；其中一部分被指定为控制站，判定与干预只发生在控制站。
- 车次号：正整数，不可变；车次号先后即计划发车次序，任何干预不改变已发车次序。
- 运行次序键：内部为不可约分有理数，备车插入时取相邻两键的中位分数，保证
  新车次严格位于前后两车之间而无需重编号。
- 时刻：`int64` 秒。所有带 `at` 的操作都不得早于最近一次被接受操作的时刻。

## 操作（均为 Service 方法）

- `New(Config)`：校验方案（站数、目标间隔、停站/行驶时长、扣车上限、容忍、工时）。
- `RegisterDriver(id)`：登记司机。
- `AddBackup(trip, driver, at)`：带司机的备车入池（尚未进入运行序列）。
- `ScheduleTrip(trip, driver, at)`：在首发站按计划时刻投放一辆车。
- `RegisterAlight(trip, station, at)`：登记某站存在下车请求（幂等）。
- `ReportArrival(trip, station, at)`：上报到站；必须站序严格递增、不跨非跳过站。
  到达控制站时立即完成串车/大间隔判定，大间隔自动尝试插入备车。
- `Intervene(trip, station, ActionHold|ActionSkip)`：对“已判串车”的车次在其
  当前控制站显式施加扣车或跳站。
- `Query(trip, station)`：O(1) 返回到离时刻、判定（`Verdict`）、干预类别
  （`Action`）与降级原因（`Reason`）。

## 判定与降级规则

- 串车：与前一车次在本站到站差 `2*gap < TargetHeadway`（恰等于一半不算）。
- 大间隔：`gap > 2*TargetHeadway`（恰等于两倍不算）。
- 扣车链：需求受 `HoldCap` 限制 → 工时闸（超上限 `duty-limit`，仅降级）→
  最晚离站闸（`计划到站 + DepartureTolerance`，超则改判跳站）。
- 跳站：越过本控制站到下一控制站之间的非控制站；途中有下车请求则整次拒绝；
  每车次最多一次。
- 插入备车到站时刻 `max(前车到站+h, 当前时钟)`，取池中最小车次号。

## 错误类别（只返回次序最靠前的一类）

`ErrInvalidArgument` → `ErrClockRollback` → `ErrTripNotFound` →
`ErrStationNotFound` → `ErrOutOfOrder` → `ErrDuplicateReport` →
`ErrNotBunched` → `ErrDriverNotFound`。工时超限不是错误，而是可查询的降级原因。

## 可复现与验证

- 所有写操作在一把锁内串行化；并发调用等价于某个串行顺序，相同操作序列重放
  得到完全相同的到离时刻与干预记录。
- `model.go` 中的 `NaiveModel` 是独立编写的朴素逐车逐站模型；`diff_test.go`
  用随机 `Op` 序列逐步对照两者的错误与完整快照，并打印每条操作日志。
- 查询 O(1)：`BenchmarkQueryConstantTime` 在 100 / 10000 条记录下同为常数级。
