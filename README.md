# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 编号池（`idpool` 包）

`idpool` 提供编号 1..N 的线程安全编号池，带「隔离期 + 短命加倍」机制。

### 隔离期的加倍与重置

- 每个编号维护「上一次隔离期」，新编号初值为基础隔离期 `Q`；上限为 `Qmax`（`Qmax >= Q`）。
- 释放使用中的编号时，令存活时间 = 释放时刻 − 分配时刻：
  - 存活时间 **小于** 短命阈值 `R`（整数毫秒）视为短命：本次隔离期 = `min(2 × 上一次隔离期, Qmax)`；
  - 存活时间 **大于等于 R**（恰好等于 R 也算正常寿命）：本次隔离期重置为 `Q`。
- 「上一次隔离期」始终更新为本次释放实际使用的隔离期；因此连续短命会逐次翻倍 `Q → 2Q → 4Q → …` 并封顶于 `Qmax`，一旦经历一次正常寿命即回到 `Q`，之后再次短命重新从 `Q` 开始翻倍。
- 编号在 `释放时刻 + 本次隔离期` 起变为空闲；恰在该解除时刻即可再次分配，因此同一编号相邻两次分配的时刻差不小于其间那次释放的隔离期。

### 分配选择规则

- 状态为空闲的最小编号优先（最小堆）；隔离中的编号在解除时刻之前绝不借出。
- 没有空闲编号时分配失败：
  - 若有隔离中的编号，返回 `ErrNoFree`，并在 `AllocationFailure` 中给出最早解除时刻与对应编号；多个编号同一时刻解除时取编号最小者；
  - 若全部编号都在使用中，返回 `ErrExhausted`（`AllocationFailure.Exhausted = true`）。
- 任意时刻「使用中 + 隔离中 + 空闲」的编号数恒为 N。

### 拒绝规则（整体拒绝，不改变任何状态与隔离记录）

- 构造参数非法：`N <= 0`、`Q <= 0`、`Qmax < Q`、`R <= 0` 返回 `ErrInvalidConfig`。
- 调用时刻早于此前任何已接受操作的时刻：返回 `ErrClockRollback`，且时钟回拨优先于其他释放错误判定。
- 释放其余错误按顺序只报第一个：`ErrOutOfRange`（编号越界）、`ErrFreeID`（编号空闲）、`ErrQuarantinedID`（编号隔离中）。
- 所有方法由同一把互斥锁保护，并发调用结果等价于某个串行顺序；逻辑无随机量，相同操作序列重放得到完全相同的编号序列。

### API 概览

```go
p, err := idpool.New(n int, baseQ, maxQ, shortLivedThresholdR int64) (*Pool, error)
id, err := p.Allocate(now int64) (int, error)
id, detail, err := p.AllocateDetailed(now int64) (int, AllocationFailure, error)
err := p.Release(id int, now int64) error
snap, err := p.Query(now int64) (Snapshot, error)
```

时刻单位为整数毫秒；`Query` 返回的 `Snapshot` 中各编号列表均按编号升序，并附带最早解除时刻/编号。

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
go test -v ./idpool

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 本地验证编号池

```bash
# 全部用例（含输入/输出/判定依据日志）
go test -v ./idpool

# 竞态检测 + 重复执行（并发串行化、回放确定性）
go test -race -count=10 ./idpool
```

关键用例：恰在解除时刻分配（`TestMinFreeAndReadyBoundary`）、短命逐次翻倍并封顶 `Qmax`、一次正常寿命后重置为 `Q` 且再次短命从 `Q` 翻倍（`TestDoublingCapAndReset`）、存活恰等于 `R`（`TestLifeExactlyR`）、池耗尽与并列取小（`TestExhausted`、`TestMinFreeAndReadyBoundary`）、拒绝优先级与无副作用（`TestRejectionPriorityAndNoMutation`）、重放一致性（`TestDeterministicReplay`）、并发不变量（`TestConcurrentSerialEquivalence`）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
