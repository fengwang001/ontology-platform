# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## mvcc：带子事务与命令号的元组版本可见性判定器

`mvcc` 包提供 `Store`，登记事务树状态并按快照与命令号判定元组版本可见性。所有方法均可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。

### 事务状态与有效状态

- 事务号为正整数。`Begin(x)` 开始顶层事务，`BeginSub(x, p)` 在运行中的事务 `p` 下开始子事务。
- 事务状态四种：运行中、已子提交（仅子事务，`CommitSub`）、已提交（仅顶层，`Commit`）、已中止（`Abort` 把目标及其全部后代置为已中止）。两种提交都要求目标运行中且无运行中的后代。
- 有效状态（判定可见性与删除标记冲突时使用）：
  - 自身已中止 → 「中止」；
  - 自身已子提交或已提交，且其根（顶层祖先）已提交 → 「提交」；
  - 其余 → 「活动」。

### 快照与命令号

- `Snapshot()` 返回从 1 起连续递增（无空洞）的快照编号，并记录此刻所有已提交顶层事务号集合。
- 命令号为非负整数，同一事务树内所有写操作（`Insert`/`Delete`）的命令号必须非递减；被拒绝的写操作不会推进该树已用最大命令号。

### 可见性判定规则

`Visible(t, x, c, s)`：观察者 = 运行中事务 `x`、当前命令号 `c`、快照编号 `s`。

- 事务 `y` 带命令号 `cy` 的效果「被看到」，当且仅当：
  - `y` 与 `x` 同根：`y` 的有效状态不是「中止」且 `cy < c`（严格小于，故同命令号的写本命令内不可见，下一命令可见）；
  - `y` 与 `x` 不同根：`y` 的有效状态是「提交」且 `y` 的根在快照 `s` 的已提交集合内（快照之后才提交的根不可见）。
- 元组 `t` 可见 ⟺ 其 `xmin` 的效果被看到，且 `xmax` 的效果未被看到（无删除标记视为未被看到）。
- `Explain` 与 `Visible` 判定一致，额外返回人类可读的判定依据。

### 错误优先级

每个操作按下列顺序只报告第一个匹配的拒绝原因（哨兵错误见 `mvcc/errors.go`，可用 `errors.Is` 判定）：

- `Begin`：`ErrTxIDNotPositive` → `ErrTxExists`
- `BeginSub`：`ErrTxIDNotPositive` → `ErrTxExists` → `ErrParentNotFound` → `ErrParentNotRunning`
- `CommitSub`/`Commit`/`Abort`：`ErrTxNotFound` → `ErrTxNotRunning` → `ErrTxTypeMismatch`（仅两种提交）→ `ErrTxHasRunningDescendants`（仅两种提交）
- `Insert`/`Delete`：`ErrTxNotFound` → `ErrTxNotRunning` → `ErrNegativeCommandID` → `ErrCommandIDTooSmall` → `ErrTupleExists`（Insert）/ `ErrTupleNotFound`（Delete）→ `ErrTupleDeleted`（Delete，标记者有效状态非「中止」时；已「中止」可覆盖）
- `Visible`：`ErrTxNotFound` → `ErrTxNotRunning` → `ErrNegativeCommandID` → `ErrUnknownSnapshot` → `ErrTupleNotFound`

被拒绝的操作不改变任何状态。

### 本地验证

```bash
go test ./mvcc/            # 确定性场景 + 2000 组随机序列与朴素判定器对拍
go test -race -v ./mvcc/   # 竞态检测；随机对拍逐条打印输入、输出与判定依据
```

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
