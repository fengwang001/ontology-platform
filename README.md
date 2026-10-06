# 铁路区段席位复用与分站票额系统

这是一个可并发调用、结果确定可复现的 Go 库。列车按站序保存发车时刻，同一座位可在左闭右开且不重叠的区段上复用；到站等于下一票发站时允许相接。

## 模块划分

- `system.go`：系统门面、全局时钟、拒绝次序、购票/退票事务和并发串行化。
- `train.go`：列车、车站、座位、发到站票额矩阵等不可变配置与状态构造。
- `seatmap.go`：座位 × 相邻站间边占用矩阵和三级相接优先选座。
- `quota.go`：分站票额、截止并入、共用票额退回和无座名额。
- `errors.go`：唯一错误码与可程序化判断的错误类型。
- `railway_test.go`：规则边界、拒绝次序和并发测试。
- `model_test.go`：独立朴素模型、随机操作重放和逐步判定日志。

## 快速使用

```go
sys := railway.NewSystem()
err := sys.AddTrain(railway.TrainConfig{
    ID:             "G1",
    Stations:       []string{"A", "B", "C"},
    Departs:        []int64{100, 200, 300},
    Seats:          []railway.Seat{{Car: 1, No: 1}},
    Quota:          [][]int{{0, 2, 2}, {0, 0, 2}, {0, 0, 0}},
    SharedQuota:    1,
    AdvanceSeconds: 30,
    StandingRatio:  1,
})
result, err := sys.Buy(railway.BuyRequest{
    Time: 1, TrainID: "G1", TicketID: "t1",
    Passenger: "张三", Origin: 0, Destination: 2,
    AcceptStanding: true,
})
err = sys.Refund(railway.RefundRequest{Time: 2, TrainID: "G1", TicketID: "t1"})
```

`BuyResult.Seat == nil && Standing == true` 表示无座票；`UsedSharedQuota` 表明本次票额是否来自共用票额。

## 关键规则

- 选座优先级：两端恰好相接、仅一端相接、两端不相接；同级按车厢号、座位号。
- 有座票先扣发到站分配票额，分配为零再扣全车共用票额。
- 当前时刻不小于 `发站发车时刻 - AdvanceSeconds` 时，该发站剩余票额不可逆并入共用。
- 有空闲座位一定出售有座票；只有全程无可用座位且接受无座时才检查各相邻边名额。
- 退票按原始票源退回；若原分配票额已并入共用，则退回共用。
- 购票与退票时刻必须严格早于票面对应发站发车时刻，相等即已发车。
- 拒绝码顺序：参数非法、时钟回退、列车不存在、车票不存在、车票已退、已发车、乘车人区段相交、票额不足、无席位。

## 复杂度

- 选座和全程空闲判定为 `O(S×N)`，`S` 为座位数，`N` 为站数；不扫描已售车票。
- 无座名额检查为 `O(N)`。
- 剩余票额查询只访问固定大小矩阵和按发站计算截止视图，与已售车票总数无关。
- 所有变更通过同一互斥锁串行化，因此相同操作序列重放得到相同座位和票额结果。

## 验证

当前环境的 Go 在 `/usr/local/go/bin`，且默认缓存目录不可写，可使用：

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -race -v ./...
PATH=/usr/local/go/bin:$PATH go vet ./...
gofmt -w *.go
```

随机对照测试使用固定种子，并逐段 `t.Logf` 打印输入、输出、错误码和判定依据；朴素模型通过扫描票据独立实现，不调用生产选座逻辑。更多取舍见 `DESIGN.md`。
