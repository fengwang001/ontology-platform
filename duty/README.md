# duty — 机组值勤时限与排班合规

时刻为非负整数分钟，值勤期为左闭右开区间 `[Start, End)`。

## 配置 `Config`

- 一天 `DayLen` 分钟，按 `DayBoundary1/DayBoundary2` 划分为早/日/夜三个左闭右开时段。
- `BaseLimit`：三时段基础单次值勤上限；每多一个航段减 `PerLegCut`，不低于 `MinLimit`。
- `MinRest`：相邻值勤期最小休息，实际取 `max(MinRest, 前一值勤时长)`，恰等即足够。
- `Window7/Limit7`、`Window28/Limit28`：任意位置滚动窗与累计上限。
- `MaxExtension`：单次最大延长量。

## API

```go
func NewSystem(cfg Config) *System
func (s *System) AddPerson(now, personID int) Result
func (s *System) UpdateQualification(now, personID, aircraft, expiry int) Result
func (s *System) RevokeQualification(now, personID, aircraft int) Result
func (s *System) Register(now, personID, start, end, legs, aircraft int) (Result, Accepted)
func (s *System) Extend(now, personID, dutyID, newEnd int) Result
func (s *System) Cancel(now, personID, dutyID int) Result
func (s *System) EarliestReport(personID, at, legs, aircraft int) (minute int, ok bool)
func (s *System) Duties(personID int) []Duty
```

`Result` 为接受（`OK()`）或 `Reason`；滚动类拒绝带最小超限窗起点 `WindowStart`。
拒绝次序：参数非法 > 时钟回退 > 人员不存在 > 值勤期不存在 > 已开始/已解除不可改 >
资质无效 > 重叠 > 休息不足 > 单次超限 > 延长规则 > 7 日累计 > 28 日累计。

## 规则要点

- 单次时长恰等于上限合规，超一分钟即超限；端点相接视为重叠（休息为零）。
- 中间插入同时检查与前、后值勤期的休息；滚动累计按值勤期与窗的重叠长度计入。
- 延长只能一次、不超 `MaxExtension`，延长后按原上限 + `MaxExtension` 判定单次，
  并重新满足休息与累计；任意 7 日窗内至多一个已延长值勤期。
- 资质到期必须严格大于解除时刻；吊销不影响已登记值勤期。
- 已开始（`Start <= now`）不可撤销；已解除（`End <= now`）不可撤销也不可延长；
  已开始但未解除仍可延长。
- 所有操作并发安全，等价于某个串行顺序；时钟只被接受操作推进。

## 验证

```bash
go test ./...            # 全部测试
go test -race ./...      # 竞态检测
go test -v ./duty        # 详细输出（测试在 duty 目录）
```

差分测试逐步日志写入 `duty/fuzz_trace.log`。设计取舍见 `duty/DESIGN.md`。
