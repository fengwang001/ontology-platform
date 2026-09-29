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

## 增量中位数（`median` 包）

`median.Tracker` 对整数多重集的加入（Add）与撤回（Withdraw）做动态维护，
随时返回当前中位数，结果与“把全部元素排序后朴素取值”完全一致。

### 中位数定义

- 多重集按升序排列，取位置 `(n-1)/2` 的元素（0 基）。
- 奇数个取正中间；偶数个取**偏下**者（下中位数）。例如 `[1,2,3,4]` 的中位数是 `2`。
- 空多重集查询返回 `ErrEmpty`。

### 数据结构与维护规则

- 维护两个堆：
  - `small`：较小一半的大顶堆，堆顶是较小一半的最大值。
  - `large`：较大一半的小顶堆，堆顶是较大一半的最小值。
- 每侧各自记录 `valid`（有效个数），并用 `pending` 账本登记等待清理的作废副本
  （懒删除）。另用 `freq` 账本记录每个值当前的有效副本数，供整批校验与撤回定位。
- 不变量（任意操作结束后成立，`Check` 可自检）：
  - `small.valid == large.valid` 或 `small.valid == large.valid + 1`（两侧差不超过一）。
  - 较小侧每个有效元素都不大于较大侧（`small` 堆顶 `<= large` 堆顶）。
  - 中位数即 `small` 的堆顶。
- **加入**：与 `small` 堆顶比较，`v <= small.top()` 入 `small`，否则入 `large`，随后再平衡。
- **撤回**：先在 `freq` 上确认该值当前确实存在；存在则把一个副本登记为作废副本
  （对应侧 `pending++`、`valid--`），立刻清理堆顶并再平衡。作废副本只有浮到堆顶时
  才会被 `prune` 弹出，因此允许在堆深处短暂等待。
- **再平衡**：`small` 偏多（`small.valid > large.valid+1`）则把其有效堆顶移到 `large`；
  `large` 偏多（`large.valid > small.valid`）则反向移动。移动的都是已确认有效的堆顶。

### 整批提交与失败不留痕

- `Commit([]Op)` 原子地提交一批 `OpAdd` / `OpWithdraw`；`Add`、`Withdraw` 是其单条快捷方式。
- 提交分两阶段：先在临时净增量账本上模拟并校验整批（不触碰堆与计数），全部通过后才应用。
- 任意一条被拒，整批拒绝，两个堆、两侧有效个数、`freq`、总长度均不发生任何变化。
- 失败原因通过 `errors.Is` 区分，且互不相同；批内具体失败位置由 `*OpError`（含 `Index`）标注。

| 错误哨兵 | 触发场景 |
| --- | --- |
| `ErrInvalidArgument` | 空批次、未知操作类型等非法参数 |
| `ErrEmpty` | 空多重集上调用 `Median` |
| `ErrNotFound` | 撤回一个当前不存在的值（即使其作废副本仍在堆中等待清理） |
| `ErrLimitExceeded` | 提交后有效元素个数超过容量上限（`NewTracker` 设定，默认 1,000,000） |

### 并发

- 使用 `sync.RWMutex`：`Median`、`Len`、`Check` 取读锁，可被多个执行体并发调用；
  `Add` / `Withdraw` / `Commit` 取写锁，可与查询并发执行。
- 写路径在释放锁前保证两堆堆顶为有效元素，故只读路径直接读取 `small` 堆顶即可。

### 使用示例

```go
tr := median.NewTracker(0) // 0 表示使用默认上限
_ = tr.Add(3)
_ = tr.Add(1)
_ = tr.Add(4)
m, _ := tr.Median() // 3
err := tr.Withdraw(99) // errors.Is(err, median.ErrNotFound)
```

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（逐步打印输入、中位数与“排序后朴素取值”的判定依据）
go test -race -v ./median

# 覆盖率
go test -cover ./median
```

测试覆盖：偶数取下中位数、奇偶切换、重复值只撤回一个、作废副本在堆顶/堆深处的清理、
非法参数、空集查询、撤回不存在值、超限、混合批次整批拒绝且状态不变，以及 2000 步
随机操作与朴素排序参照逐点比对和多读写者并发（`-race`）。
