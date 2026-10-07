# 高速公路门架计费服务

Go 实现位于 `tolling/`，用确定性路径推定、可审计调整记录和自然月月账复现出口结算与后续补扣/退款。

## 核心模型

- `NewNetwork`、`AddGate`、`AddEdge` 建立有向门架路网；边费率按车型区分。
- `NewService` 配置重复窗口、迟到补扣期限、月度封顶和时区。
- `RegisterVehicle` 登记车辆；`ChangeVehicleClass` 携带车型生效时刻。
- `RegisterGate` 接收入口、出口或中间门架记录；记录内时刻决定乱序语义，操作时刻驱动单调时钟。
- `RetrySettlement` 可重试此前路径不可达的未结算行程；`CloseTrip` 人工关闭。
- `Trip`、`Adjustments`、`Records` 查询当前路径、金额、调整依据和有效/重复/孤立/超期记录。
- `Month` 查询车辆自然月实收、因封顶未收和无法退还金额。

## 计费语义

- 有效锚点按记录时刻排序；相邻锚点间选择总费用最低路径，同分取门架序列字典序最小。
- 迟到中间记录在入口/出口闭区间内且未超过补扣期限时触发全量重推。
- 同一车辆、同一行程、同一门架在重复窗口内的后续记录只登记不参与重算；间隔恰等于窗口时独立。
- 每段以经过起点门架时刻对应的车型计费；生效时刻相等时采用新车型。
- 月度补扣受封顶约束；退款不会使当月实收低于零，未收和退不回金额分别可查。
- 所有错误使用 `ServiceError.Code` 区分，校验顺序保证只返回最靠前的一类错误。

## 并发与可复现

写操作在服务锁内校验时钟并串行化，查询在锁内复制路径与调整明细。任意并发调用都等价于某个合法串行顺序；相同操作序列使用同一确定性路径算法得到相同结果。

`Trip` 通过 trip ID 直接定位并只复制该行程数据，不遍历该车辆历史行程；`TestTripLookupDoesNotGrowWithVehicleHistory` 用基准分配数提供可验证证明。

## 测试

默认 Go 缓存目录在当前环境不可写时使用 `/tmp`：

```bash
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test ./...
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go vet ./...
```

测试覆盖等费字典序、迟到时刻与期限边界、重复窗口边界、车型生效边界、月封顶、退款到零、乱序到达终态、并发安全，以及 60 组随机路网/操作序列与独立朴素全量重算模型对照。随机测试用 `t.Logf` 输出每条操作的输入、输出和判定依据，可用 `go test -run TestRandomReplayMatchesNaiveFullRecomputation -v` 查看。

设计取舍见 `DESIGN.md`。
