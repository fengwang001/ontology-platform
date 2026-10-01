# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 检查点刷脏节奏调度器

`Scheduler`（见 `checkpoint.go`）按时间与日志两条进度，算出一次检查点
开始后「累计应已写出的脏页数」配额 `Q`，供刷脏节奏控制使用。

### 构造

```go
s, err := ontology.NewScheduler(T, W, F)
```

- `T`：检查点间隔（毫秒），必须为正。
- `W`：日志预算（字节），必须为正。
- `F`：完成目标（千分数），取 `1..999`。

任一参数非法时返回带可区分 `Reason` 的 `*ontology.Error`，对象不会被创建。

### 配额规则：两条进度取最大

`Begin(now, walPos, dirty)` 记录起点 `(s, w0) = (now, walPos)` 与开始时脏页
总数 `N = dirty`。`Tick(now, walPos)` 令

- 已用时间 `e = now - s`，已产生日志 `u = walPos - w0`；
- 时间配额 `q1 = min(N, ⌊N·e·1000 / (T·F)⌋)`；
- 日志配额 `q2 = min(N, ⌊N·u·1000 / (W·F)⌋)`；
- 累计配额 `Q = max(q1, q2)`，`need = Q − 已写出数`（不小于 0）。

含义：只要「时间走完目标比例」或「日志写完目标预算」任一达成，就应刷够
相应页数；两条进度相互独立、各管各的，取二者最大不会因一条进度停滞而拖慢
另一条。

### 取整与封顶

- 除法全部为精确整数运算并**向下取整**（floor），中间乘积用 `math/big`
  计算：`N`、`e`、`u` 可达 `2^40` 量级，`N·e·1000` 远超 int64，绝不溢出。
- `q1`、`q2` 各自封顶于 `N`，因此在目标时刻（如 `e = T·F/1000`）`q1`
  恰为 `N`；早一毫秒则是严格的向下取整值。
- 输入单调不降时，同一检查点内 `Tick` 返回的 `Q` 单调不降。

### 完成条件与状态

- `Wrote(k)` 登记又写出 `k` 页；`k` 必须为正且不得超过剩余未写出数
  `N − 已写出数`。
- 已写出数达到 `N` 时检查点立即完成：调度器回到空闲态（`N`、已写出数归零），
  已完成检查点数加一。
- `Begin(..., dirty=0)` 立即完成，不进入进行态，同样计入完成数。
- 检查点之间没有最小间隔：回到空闲后可用不小于此前已接受值的 `now`/`walPos`
  立即开始下一次。
- `Status()` 返回 `{Active, N, Written, Completed}`，空闲时 `N`、`Written`
  均为 0。

### 单调性与并发

- `Begin` 与 `Tick` 的 `now`、`walPos` 都不得小于此前**任一次已被接受调用**
  所给的对应值（跨检查点同样生效）；倒退调用被整体拒绝。
- 被拒绝的操作不改变任何状态，包括最近时刻、最近日志位置与进行中的进度。
- 所有方法均在互斥锁下串行化，并发调用的结果等价于某个串行顺序；任意时刻
  已写出数不超过 `N`。

### 错误原因

`*ontology.Error` 的 `Reason` 字段逐操作、按规定顺序只报第一个错误：

- 构造：`T_not_positive`、`W_not_positive`、`F_out_of_range`。
- `Begin`：`negative_now`、`negative_wal_pos`、`negative_dirty`、
  `checkpoint_active`、`now_regression`、`wal_pos_regression`。
- `Tick`：`negative_now`、`negative_wal_pos`、`no_checkpoint`、
  `now_regression`、`wal_pos_regression`。
- `Wrote`：`no_checkpoint`、`k_not_positive`、`k_exceeds_remaining`。

### 本地验证

```bash
# 全部测试（-v 可查看 2000 组随机对拍的输入/输出/判定依据日志）
go test -race -v ./...

# 仅跑与 big.Rat 朴素公式的随机对拍
go test -run TestRandomDifferential -v .

go vet ./...
gofmt -l .
```

测试内容包括：目标时刻边界取整、两条进度取最大、双向封顶、`N=0` 立即完成、
`N=e=2^40` 且 `T=F=1` 不溢出、`Wrote` 超量拒绝、`now`/`walPos` 各自倒退拒绝、
被拒操作不改状态，以及 2000 组随机操作序列与 `big.Rat` 朴素实现的逐步对拍
（含常规与 `2^40` 大数量级两种画像），日志打印每条输入、输出与判定依据。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
