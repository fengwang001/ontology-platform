# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 带重置检测的累计计数器（`counter` 包）

`counter.Tracker` 按独立序列接收累计读数 `(t, v)`（`t` 为 int64 时间戳，
`v` 为非负 int64），并对左开右闭窗口 `(a,b]` 求增量与重置次数。

### 重置判定与单对增量

对相邻两个读数 `(p, q)`：

- `q.v >= p.v`：正常增长，该对增量为 `q.v - p.v`；
- `q.v < p.v`：判定为一次重置，该对增量为 `q.v`
  （视为重置后从 0 起算；重置发生前未上报的部分不补）。

序列的第一个读数没有前驱，不产生增量，也不计重置。重置读数恰好为 `0` 时，
该对增量为 `0` 但仍计一次重置；连续两次重置各自计数、互不影响。

### 窗口归属口径

窗口 `(a,b]` 只按相邻对中**后一个读数** `q` 的时间戳归属：

- `q.t` 恰好等于 `a`：不计入；
- `q.t` 恰好等于 `b`：计入；
- 前一个读数 `p` 是否在窗口内不影响归属。

因此每条相邻对恰好归属唯一的单点归属区间，对任意 `a < m < b` 恒有
`Query(a,b) == Query(a,m) + Query(m,b)`，增量与重置次数分别成立，
窗口可任意拆分相加。序列存在但窗口内无相邻对时返回零值结果而非错误。

实现为每个序列保存时间戳、原始值、u128 增量前缀和与重置次数前缀和，
查询通过两次二分做常数时间区间和；前缀和使用 128 位无符号整数，
只有在窗口和本身超出 int64 时才返回 `ErrOverflow`（查询期判定，不改状态）。

### 写入与错误原因

错误均可通过 `errors.Is` 区分：

- `ErrNegativeValue`：写入值为负（写入时先于时间次序检查）；
- `ErrTimestampOutOfOrder`：时间戳小于该序列最新读数；
- `ErrValueConflict`：与最新读数同时间戳但值不同；
- `ErrInvalidWindow`：查询窗口 `a >= b`（查询时先于序列不存在检查）；
- `ErrSeriesNotFound`：查询从未出现过的序列；
- `ErrOverflow`：窗口增量之和溢出 int64。

同时间戳同数值的重复写入是幂等的：成功且不改变任何状态；
任何被拒绝的操作都不会改变任何序列。`Write`/`Query` 可并发调用，
内部由读写锁保护，结果等价于某个合法的串行交错。

### 本地验证

```bash
# 全量测试（含与朴素实现的随机对照、窗口拆分恒等式、竞态检测）
go test -race -v ./counter/

# 只看结论
go test -race -count=1 ./...

# 若默认 GOCACHE 只读，可指定缓存目录：
GOCACHE=/tmp/gocache go test -race ./counter/
```

`counter_test.go` 中每个用例均用 `t.Logf` 打印输入、输出与判定依据
（`go test -v` 可见），覆盖：读数恰在 `a` 不计入、恰在 `b` 计入、
重置读数恰为 0、相邻两次重置、重置读数落在窗口边界、首个读数落入窗口
不产生增量、幂等重复与冲突重复、错误优先级、溢出、并发可串行化，
以及 300 组随机序列 ×30 窗口对朴素实现和随机拆分恒等式的对照。
