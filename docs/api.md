# API 文档

## 构造监控器

```go
monitor, err := routeexecution.NewMonitor(config, stops, durations,
    routeexecution.WithOperationLogger(func(log routeexecution.OperationLog) {
        // 打印或持久化输入、输出、错误原因和完整快照
    }),
)
```

`TravelDuration.Known` 必须为 `true`。当前构造器要求提供起点到各站以及任意两个不同站点之间的时长，时长允许非对称。

## 上报实际到达

```go
err := monitor.ReportArrival("B", actualArrivalSeconds, operatedAtSeconds)
```

返回哨兵错误：

- `ErrClockRollback`
- `ErrStopNotFound`
- `ErrInvalidState`
- `ErrInvalidOrder`

使用 `errors.Is` 判断错误类型；状态错误的包装消息包含站点 ID 和具体原因。

## 取消站点

```go
err := monitor.CancelStop("B", operatedAtSeconds)
```

只有尚未上报、尚未到达、尚未被硬窗跳过的预测站点可取消。取消后从对应后缀重新推演。

## 读取快照

```go
snapshot := monitor.Snapshot()
```

`StopResult` 包含：

- `Status`：准时、等待后准时、软窗迟到、跳过、取消。
- `ArrivalSeconds`、`ServiceStartSeconds`、`DepartureSeconds`。
- `Reported`：是否已接受实际到达上报。
- `PublishedETA` 与 `HasPublishedETA`：对外发布值。

`HasArrival`、`HasService`、`HasDeparture` 用于区分跳过或取消站点中为零的合法时刻与“不存在该时刻”。

