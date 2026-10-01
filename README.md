# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分层瀑布分账器（`waterfall` 包）

带追回（clawback）的分层瀑布分账器，位于 `waterfall/waterfall.go`。

### 配置

- `waterfall.New([]Layer{...})` 接收按顺序排列的至少两层：
  - 前 n-1 层为有上限层，`Cap` 为该层累计可收额（非负 int64）。
  - 最后一层为无上限的余额层，`Fee` 为管理人提成百分比（0-100 的整数）。
- 每层内部记录已收额 `recv`；构造时复制入参，外部修改不影响分账器。

### 瀑布填充（`Allocate(x)`）

- `x` 必须为正整数。
- 从第 0 层起依次填入 `min(剩余额, Cap-recv)`，剩余转入下一层。
- 余额层吞下全部剩余（前层已满时新分配直接落余额层）。
- 追回后再分配总是**从第一个有空位的层重新开始填**，而不是接着上次停下的层。

### 逆序追回（`Clawback(y)`）

- `y` 必须为正整数，表示撤销已分配的总额。
- 从最后一层起逆序扣减，每层扣 `min(剩余追回额, recv)`，扣完为止。
- 追回使前层空出位置后，后续 `Allocate` 会先补前面的层。

### 累计口径拆分（`Split()`）

余额层的拆分**始终按其当前累计已收额 R 重算**，不按单笔增量拆分后累加：

- 管理人：`floor(R * g / 100)`
- 出资人：`R - 管理人`

例如 `g=20`、余额层先后收到 `4` 与 `4`：逐笔口径得 `0+0=0`，
累计口径得 `floor(8*20/100)=1`；实现采用累计口径，追回后同样按新的 R 重算
（R 由 10 追回至 5 时，管理人由 2 降至 1）。

### 查询

- `Snapshot()`：各层已收额副本（各自原子）。
- `Total()`：累计已分配总额（各层之和）。
- `Split()`：管理人 / 出资人拆分。
- `State()`：在同一个读锁内返回 `(recv, total, manager, investor)` 的一致快照，
  组合查询时应使用本方法（分别调用三个 getter 之间可能穿插写操作）。

### 拒绝原因（整体拒绝，不改变任何已收额）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | 层数小于 2 |
| `ErrNegativeCap` | 前层 `Cap` 为负 |
| `ErrFeeOutOfRange` | `Fee` 不在 0-100 |
| `ErrAmountNotPositive` | 分配/追回金额不为正（最优先判定） |
| `ErrClawbackExceedsRecv` | 追回额超过当前已分配总额（与「不为正」是不同原因，不为正先判） |
| `ErrTotalExceedsLimit` | 分配后已分配总额将超过 `1e15`（`MaxTotal`） |

### 并发与确定性

- 所有方法由 `sync.RWMutex` 保护，可并发调用，结果等价于某个串行顺序。
- 任意时刻各层已收额之和 = 累计分配 - 累计追回；非末层 `recv <= Cap`。
- 相同操作序列重放得到完全相同的各层已收额与拆分结果。

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

# 瀑布分账器（带竞态检测与逐步判定日志）
go test -race -v ./waterfall

# 仅跑朴素模拟对照与确定性重放
go test -race -v -run 'NaiveFuzz|Concurrent' ./waterfall
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

若默认构建缓存目录只读，可指定缓存目录：

```bash
GOCACHE=/tmp/gocache go test -race ./...
```
