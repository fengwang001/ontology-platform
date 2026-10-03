# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前包含 `roadnet` 包：带通告历史的时间依赖路网最早到达查询服务。

## 环境要求

- Go 1.26+（`go version` 确认）

## roadnet：时间依赖路网最早到达查询

在有向路网上，边的通行耗时随出发时刻分段变化、可封闭、可随时间发布
新耗时（通告）。查询允许在任意节点等待任意长时间，返回最早到达时刻、
确定的路线与各段出发时刻；所有结果可精确复现。

### 耗时剖面与通告记录

- 耗时剖面是 1–32 个分段 `(偏移, 耗时)`：第一个偏移必须为 0，偏移严格
  递增；耗时为 1–10^6 的整数，或 −1 表示封闭（该段内不能开始通行）。
  最后一段延伸到无穷。
- 一条通告记录是 `(生效时刻 eff, 剖面)`：剖面第 i 段在绝对时刻
  `eff+偏移` 起、到下一段起点（或下一条记录的 eff）前有效——即每段
  会被下一条记录的生效时刻截断。
- 边在出发时刻 `t` 的耗时，取“eff 不大于 t 的最后一条记录”的剖面在
  `t` 处的分段值。
- `AddEdge` 登记 eff=0 的首条记录；`Announce` 追加记录：`eff >= now`
  （不能改写过去）、`eff` 不小于该边最后一条记录的 eff；相等时替换
  （旧记录保留供历史查询，且不占 64 条上限），否则追加；每条边至多
  64 条记录。

### 等待下的到达函数

到达边终点的最早时刻为 `arr_e(t) = min(t' + 耗时(t'))`，`t'` 取所有
不小于 `t` 且不在封闭段内的整数出发时刻。因为每个分段内 `t'+耗时`
随 `t'` 严格增加，最优出发点只可能是 `t` 本身或某个后续分段的起点
（含后续记录的首段起点）；若 `t` 之后所有分段都封闭，则该边不可用。
等待可行时该到达函数关于 `t` 单调不减（FIFO），因此可以用 Dijkstra
求最早到达，在目标节点被确定（弹出）时立即停止；`popped` 计数不超过
满足 `d(x) <= d(g)` 的节点数。

### 紧边与路线取法

边 `(a,b)` 当且仅当 `arr_e(d(a)) == d(b)` 时称为紧边。返回的路线只
用紧边：在所有 `s→g` 的紧边路径中先取边数最少者、再取边编号序列字典
序最小者（按位置贪心：每一步在仍能补全为最短边数路径的紧边中选最小编号）。
每段给出 `(边编号, 出发时刻, 到达时刻)`，出发时刻取不小于 `d(起点)`
且使到达时刻等于 `arr_e(d(起点))` 的最小整数；相邻段满足
`到达时刻 <= 下一段出发时刻`，末段到达时刻等于 `d(g)`。

### 时钟、版本与通告规则

- 系统时钟 `now` 初值 0，`Advance(t)` 单调推进（`t >= now`），不产生
  新版本。
- 版本号初值 0，每次被接受的 `AddEdge`/`Announce` 加一。每条边与每条
  记录记下登记版本；被替换的记录记下被替换时的版本。
- 查询 `EarliestArrival(s, t0, g, ver)`：`ver` 缺省为当前版本；边在
  登记版本及之后可见，记录在 `登记版本 <= ver` 且（未替换或
  `替换版本 > ver`）时可见。因此对任一已产生的版本，同一查询在之后
  任意多次操作前后返回逐字相同的结果；`ver` 大于当前版本时报
  “版本尚未产生”。
- 所有方法可并发调用（`sync.RWMutex`），结果等价于某个串行顺序；
  查询不会看到只登记了一半的通告。
- 错误原因可区分（`errors.Is` 判定）：`ErrInvalidParam`、`ErrEdgeLimit`、
  `ErrEdgeNotFound`、`ErrRetroactive`、`ErrOutOfOrder`、`ErrRecordLimit`、
  `ErrClockBack`、`ErrUnreachable`、`ErrVersionFuture`，并按合同规定的
  顺序只报第一个；被拒绝的操作不改变路网、记录、`now` 与边编号。

## 运行

```bash
# 复现合同中的完整示例（含历史版本查询）
go run ./cmd/roadnet-demo
```

## 测试与本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（日志打印输入、输出与判定依据）
go test -race -v ./...

# 关键用例
go test -run TestSpecExample -v ./roadnet        # 合同示例逐条走查
go test -run TestRandomCrossCheck -v ./roadnet   # 2000 组随机路网对照朴素逐时刻 Dijkstra
go test -run TestPoppedStaysLocalOnGrid -v ./roadnet  # 网格上 popped 远小于节点总数
go test -run TestConcurrentUse -race -v ./roadnet     # 并发等价于串行

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
