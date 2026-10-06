# traffic — 事件影响传播与排队回溢推演

事件驱动、有理精确的路网排队回溢推演服务。

## 模型

- 路网：节点 + 有向路段，每条路段有长度、通行能力、恒定到达流量（建网时校验不超过通行能力）。
- 事件：登记在某路段，带开始时刻与削减比例；同时刻多事件有效削减取**最大**。
- 排队：`到达 - 有效能力 > 0` 时以该速率增长（车辆数 = 物理排队长度 / 车长）；满载即回溢，所有直接上游的有效能力被限制为不超过本路段；只向上游传播。
- 消散：事件解除后以 `有效能力 - 到达` 的速率排空，到零解除对上游的限制；多事件先后解除按剩余事件重算。
- 等级：事件路段 1 级，回溢上游依次 +1，多路径取**最小**；未受影响为 0（查询返回 `Level=0`）。
- 精确性：所有时刻/流量/排队为 `*big.Rat`，事件之间队列线性演化，只在边界时刻跳转；`t` 恰为变化时刻时变化视为已发生；不可回退。

## 使用

```go
n := traffic.NewNetwork()
_ = n.AddLink(traffic.Link{
    ID: "A", From: "X", To: "Y",
    Length: big.NewRat(100, 1), Capacity: big.NewRat(10, 1), Arrival: big.NewRat(8, 1),
})
s := traffic.NewService(n, big.NewRat(1, 1)) // 每辆车占 1 个长度单位

_, _ = s.Register(traffic.Incident{ID: "i", LinkID: "A",
    Start: big.NewRat(0, 1), Ratio: big.NewRat(3, 4)})
_ = s.Advance(big.NewRat(20, 1))
st, _ := s.Query("A") // st.Queue 排队车辆数, st.Level 受影响等级
```

操作：`Register` / `Update(id, at, ratio)` / `Clear(id, at)` / `Advance(to)` /
`Query(linkID)`。所有写操作携带时刻，早于当前时刻返回 `ErrClockRewind`。

### 错误（按优先级，只报最靠前的一类）

`ErrInvalidArgument`、`ErrClockRewind`、`ErrLinkNotFound`、`ErrIncidentNotFound`、
`ErrIncidentAlreadyEnded`、`ErrRatioOutOfRange`、`ErrArrivalExceedsCap`
（最后一项在 `AddLink` 时校验）。

## 查询复杂度

`Query` 仅做预计算状态的 map 读取，开销不随未受影响路段数增长；
全量容量/等级重算只发生在事件驱动的推进与写操作中。
见 `BenchmarkQueryManyUnaffected`。

## 并发

所有方法在同一互斥锁下串行化，并发结果等价于某个串行顺序；
固定操作序列重放得到完全相同的排队轨迹与等级。

## 模块

- `errors.go`：可区分错误类型。
- `network.go`：路网/路段/事件/查询结果类型与建网校验、上游邻接。
- `engine.go`：容量最小不动点、事件驱动精确推进、回溢翻转、等级 BFS。
- `service.go`：并发安全外观、错误优先级、时刻校验、操作日志钩子。
- `naive.go`：独立编写的朴素逐微步对照模型（仅测试使用）。

## 验证

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -count=1 ./...
go test -run TestRandomDifferential -v ./traffic/
```

`DESIGN.md` 给出关键取舍与被放弃方案。差分测试在失败时打印每条操作的
输入、输出/错误与判定依据。
