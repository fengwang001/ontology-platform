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

## mvcc：元组版本可见性判定器

`mvcc` 包实现带子事务与命令号的元组版本可见性判定（`mvcc.Engine`，全部方法可并发调用，内部以互斥锁串行化）。

### 事务状态与有效状态

- 事务号为正整数。`Begin(x)` 开始顶层事务，`BeginSub(x, p)` 在运行中的事务 `p` 下开始子事务 `x`。
- 事务状态四种：运行中、已子提交（仅子事务，`CommitSub`）、已提交（仅顶层，`Commit`）、已中止（`Abort`，级联全部后代）。
- 有效状态由事务树推导：
  - 自身已中止 => **中止**；
  - 自身已子提交或已提交，且其根（顶层祖先）已提交 => **提交**；
  - 其余 => **活动**。
- `Snapshot()` 返回从 1 起连续递增的快照编号，记录此刻所有已提交顶层事务号集合。

### 可见性判定

- 命令号为非负整数，同一事务树内写操作（`Insert`/`Delete`）的命令号必须非递减（按树根记录已用最大命令号，被拒绝的操作不改变它）。
- `Insert(t, x, c)` 建立版本 `xmin=x, cmin=c`；`Delete(t, x, c)` 打删除标记 `xmax=x, cmax=c`。标记者有效状态为「中止」时删除标记可被覆盖。
- 事务 `y` 带命令号 `cy` 的效果对观察者 `(x, c, s)` 「被看到」，当且仅当：
  - `y` 与 `x` 同根：`y` 的有效状态不是「中止」且 `cy < c`（严格小于）；
  - `y` 与 `x` 不同根：`y` 的有效状态是「提交」且 `y` 的根在快照 `s` 的集合内。
- 元组可见 <=> `xmin` 的效果被看到且 `xmax` 的效果未被看到（无删除标记视为未被看到）。

### 错误优先级

每种拒绝原因对应一个可区分的哨兵错误（`errors.Is` 判定），各操作按下列顺序只报第一个，被拒绝的操作不改变任何状态：

- `Begin`：`ErrTxNotPositive`、`ErrTxExists`
- `BeginSub`：`ErrTxNotPositive`、`ErrTxExists`、`ErrTxNotFound`、`ErrTxNotRunning`
- `CommitSub`/`Commit`/`Abort`：`ErrTxNotFound`、`ErrTxNotRunning`、`ErrTxTypeMismatch`（仅两种提交）、`ErrTxRunningDescendants`（仅两种提交）
- `Insert`/`Delete`：`ErrTxNotFound`、`ErrTxNotRunning`、`ErrNegativeCommand`、`ErrCommandOrder`、`ErrTupleExists`（仅 Insert）、`ErrTupleNotFound`（仅 Delete）、`ErrTupleAlreadyDeleted`（仅 Delete）
- `Visible`：`ErrTxNotFound`、`ErrTxNotRunning`、`ErrNegativeCommand`、`ErrSnapshotUnknown`、`ErrTupleNotFound`

### 本地验证

```bash
# 场景测试 + 2000 组随机序列与朴素实现对拍（-v 打印每步输入、输出与判定依据）
go test -v ./mvcc

# 竞态检测（含并发 Delete 唯一成功、快照编号连续无空洞用例）
go test -race ./mvcc
```
