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

## 实时热度排行榜

`Leaderboard` 位于根包，构造参数为桶长 `L`、窗口桶数 `W`、上榜门槛 `M`、榜单容量 `K` 和迟滞次数 `Hs`：

```go
lb, err := ontology.NewLeaderboard(L, W, M, K, Hs)
err = lb.Add(id, delta, eventTime)
score := lb.Score(id)
snapshot, err := lb.Snapshot(now)
preview, err := lb.Peek(now)
```

### 窗口分桶与迟到事件

- 事件桶号为 `floor(t/L)`；系统时钟是所有已接受 `Add` 与 `Snapshot` 见过的最大时刻。
- 当前桶为 `floor(c/L)`，窗口桶集合为 `(cur-W, cur]`，也就是分数只累加 `cur-W+1` 到 `cur` 桶。
- `b <= cur-W` 的事件拒绝为 `ErrExpired`；其余迟到事件仍可写入对应桶。
- 时钟前进时仅从桶号最小堆弹出过期桶，并从每 ID 的窗口聚合分中扣减；`Add` 不遍历完整窗口。
- 窗口分降为 0 且不在上一榜的 ID 会从内部分数表删除，历史零分项不会持续占用内存。

### 资格迟滞

- 上一榜中的 ID，当连续低分留任次数 `hc < Hs` 时，资格线为 `M-floor(M/4)`。
- 不在上一榜，或 `hc >= Hs` 时，资格线回到 `M`；掉榜后必须重新达到 `M` 才能入榜。
- 每次 `Snapshot` 提交后，入榜项分数低于 `M` 则 `hc` 加一，否则清零；`Peek` 不增加 `hc`。
- 同一时刻重复调用 `Snapshot` 也会计次，因此可用于复现迟滞到期；重复榜的所有 `Change` 均为 0。

### 名次、变动与掉榜

- 候选先按分数降序，再按 ID 字节序升序排列。
- 名次按全部候选计算：同分同名次，名次等于严格更高分候选数加一；可能出现 `1,2,2,4`。
- 候选排序后只取前 `K` 个入榜；被截掉的候选不改变已入榜项名次。
- 连续两榜都在榜时，`Change = 上一名次 - 本次名次`；正数表示上升，负数表示下降。
- 新入榜项设置 `New=true`；上一榜有、本次无的项进入 `Dropped`，按上一名次升序返回。

### 拒绝顺序与并发

错误按优先级返回第一个：`ErrInvalidArgument`、`ErrExpired`、`ErrClockRolledBack`、`ErrScoreOverflow`。被拒绝的操作不会修改桶、时钟、上一榜或迟滞计数。所有读写由同一把读写锁串行化，对外等价于某个合法串行顺序；`Snapshot` 不会观察到部分写入的 `Add`。

### 本地验证

```bash
# 全量测试，含 2000 组随机输入与逐桶朴素模型对照
go test -count=1 ./...

# 并发原子性验证
go test -race -count=1 ./...

# 只运行随机对照并查看输入、输出和判定依据日志
go test -run TestRandomSequencesAgainstNaiveModel -v
```

如果环境的 `GOCACHE` 指向只读目录，可指定可写缓存，例如 `GOCACHE=/tmp/go-cache go test ./...`。
