# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 公平份额作业调度器（`scheduler` 包）

按历史用量衰减的公平份额调度器：各账户的累计用量 `U` 随周期边界减半，
每次派发「`U ÷ 份额` 最小」账户的队首作业，长期用量比例趋近份额比例。

### 衰减边界

- 创建时给定起点 `t0` 与周期 `P`（整数毫秒，`P` 必须为正）。
- 边界位于 `t0 + k×P`（`k` 为正整数）；时刻恰等于边界时该边界**已生效**。
- 每个边界上**全部账户**（含队列为空的账户）的 `U` 变为 `⌊U/2⌋`。
- 一次调用跨过 `k` 个边界时，先逐个生效这 `k` 个边界再处理本次调用；
  连续 `k` 次折半等价于一次 `⌊U/2^k⌋`，实现用精确位移完成，结果一致。
- 时刻早于此前见过的任一时刻（时钟回拨）整体拒绝，且优先于其他一切原因。

### 比较规则与代价计入

- 派发时在队列非空的账户中选取 `U ÷ 份额` 最小者；
  比较用交叉相乘（`U_a × 份额_b` 与 `U_b × 份额_a`），不使用浮点或整除；
  `U` 为大整数（`math/big`），乘积任意大都精确，比值并列时取账户标识升序。
- 被选中账户的队首作业出队，其代价**即刻**计入该账户的 `U`，
  因此下一次派发会立即反映本次计费。
- 派发结果包含判定依据：全部候选账户的 `U`、份额以及被选原因，可审计、可重放。

### 拒绝规则

以下情况整体拒绝并返回可区分的原因码（`scheduler.Error.Reason`），
且被拒绝的操作不改变任何 `U`、队列与已生效边界数：

- `invalid_period`：周期非正整数；`invalid_share`：份额非正整数；
- `duplicate_account`：账户重复注册；`account_not_registered`：提交到未注册账户；
- `invalid_cost`：代价非正整数；`duplicate_job_id`：作业标识重复（含已派发的）；
- `no_jobs`：全部队列为空时派发；`clock_backwards`：时刻早于此前见过的任一时刻。
- 多因同时成立时，时钟回拨优先；提交操作按「账户未注册 → 代价 → 标识重复」
  的顺序只报告第一个原因。

### 并发与可复现性

- 注册、提交、派发、查询均可并发调用，内部串行化，结果等价于某个串行顺序。
- `U` 恒为非负；每个作业恰被派发一次；相同操作序列重放得到完全相同的
  派发顺序与 `U`。

### 本地验证

```bash
# 单元测试（日志含输入、输出与判定依据）
go test -v ./scheduler

# 竞态检测
go test -race ./scheduler/
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
