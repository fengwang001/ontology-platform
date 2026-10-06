# pumpstation

`pumpstation.Controller` 根据整数毫米水位和非负整数秒时间戳控制 2 到 8 台泵。

## 配置

使用 `Config` 描述每个运行规模的启/停水位、干运行与恢复水位、溢流水位、最短运行/停机时长和最小起泵间隔；`New` 会在构造时一次性验证所有阈值和时长关系。

## API

- `ReportLevel(now int64, level int) (Evaluation, error)`：接受严格递增的水位上报并执行一次评估。
- `SetFault(now int64, pumpID int, faulted bool) (StateChange, error)`：上报故障或维修恢复。
- `SetMaintenance(now int64, pumpID int, maintenance bool) (StateChange, error)`：投入或解除检修。
- `Snapshot() Snapshot`：读取当前目标台数、干运行锁定和各泵运行/累计时长。

错误使用哨兵值 `ErrInvalidArgument`、`ErrTimeRollback`、`ErrPumpNotFound`、`ErrInvalidState`，可用 `errors.Is` 判断，优先级依次为参数非法、时刻回退、泵不存在、状态不允许。

`Evaluation.Reason` 和 `StateChange.Reason` 是给日志与测试使用的人类可读判定依据，不保证文本逐字稳定；程序逻辑应判断 `Actions`、枚举状态和哨兵错误。

## 测试

```bash
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test ./pumpstation -v
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test ./pumpstation -run '^$' -bench . -benchmem
```

随机对照测试使用固定种子，边界测试在 `-v` 日志中打印输入、输出动作和判定依据。完整设计见 `docs/pump-controller.md`。
