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

## Undo 日志段管理器（`undo` 包）

带回滚段槽位、段缓存复用与按读视图水位清理的 undo 日志段管理器，实现位于 `undo/manager.go`。

### 构造与段模型

- 构造参数：槽位数 `S∈[1,1000]`、每页记录数 `K∈[1,1000]`、总页预算 `PB∈[1,10^6]`（`NewManager(S, K, PB)`）。
- 每个事务最多两个 undo 段：插入 undo（I）与更新 undo（U）。段含 `n` 条记录时占 `max(1, ceil(n/K))` 页，并占一个槽位。
- `Begin(t)` 登记事务（事务号 `1..10^6`，重复登记含已终止者报"事务已存在"）；`Insert(t)`/`Modify(t)` 分别向 I/U 段追加一条记录。
- 申请新段：先弹该种缓存栈顶复用（`n` 清零，保留槽位与 1 页，不检查槽位）；缓存为空则先查空闲槽位、再查 1 页预算，任一不足即拒绝且不残留任何状态。追加记录跨页边界（`n+1 > K×当前页数`）时需再占 1 页，预算不足同样拒绝且不改状态。

### 两种 undo 段的释放时机

- `Commit(t)`：分配提交序号 `trx_no`（从 1 起，每次成功提交加一，无记录事务也消耗）；I 段**立即**按 Release 规则处理；U 段按 `trx_no` 追加到历史链尾部，暂不释放。
- `Rollback(t)`：事务终止，全部段立即 Release，不消耗 `trx_no`。
- Release 规则：段只占 1 页且 `4n ≤ 3K` 时压入该种缓存（保留槽位与 1 页，缓存段 `n` 保留为 Release 时的记录数，复用时清零）；否则整体释放，归还槽位与全部页。缓存为后进先出（LIFO）栈，复用总是弹栈顶。
- 已用页统计所有段（活跃、缓存、历史），任何时刻已用槽位 = 活跃段数 + 缓存段数 + 历史段数且 `≤ S`，已用页 `≤ PB`。

### 视图限值与清理限值

- `OpenView()` 返回视图号（从 1 起），其限值 `limit = 调用时已发 trx_no 数 + 1`；`CloseView(id)` 关闭，不存在或已关闭报"视图不存在"。
- 清理限值 `PL = min(全部打开视图的 limit)`；无视图时 `PL = 已发 trx_no 数 + 1`。
- `Purge(n)`（`n≥1`）从历史链头部起，当头部段 `trx_no` **严格小于** `PL` 时回收（Release）它，至多 `n` 个，遇到不满足者即停，返回按次序回收的 `trx_no` 列表。历史链 `trx_no` 严格递增，因此被回收的 `trx_no` 一定小于回收时的 `PL`。

### 错误码与并发

- 拒绝原因按序只报第一个：参数非法 → 事务不存在 → 事务已终止 → 无空闲槽位 → 页预算不足（`CloseView` 报视图不存在）。新段申请按先槽位后页预算检查；被拒绝的操作不改变任何状态。
- 所有操作与 `Snapshot()` 查询均可并发调用（内部单互斥锁串行化），结果等价于某个串行顺序；相同操作序列重放得到完全相同的槽位、缓存栈、历史链与回收序列。

### 本地验证

```bash
# 单元测试（规则边界、错误顺序、规格示例回放、并发不变量）
go test ./undo/ -v

# 2000 组随机操作序列与朴素模拟对照（日志含每步输入、输出与判定依据）
go test ./undo/ -run TestRandomAgainstNaiveModel -v

# 竞态检测
go test -race ./undo/
```
