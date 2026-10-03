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

## 多收件人消息投递回执状态机

实现在 `receipt` 包（`receipt/tracker.go`）：`NewTracker(S)` 以软退信阈值
S（1..16）构造并发安全的 `Tracker`，全部操作在互斥锁下完成，语义等价于
某种串行交错。

- `Send(msg, rcpts, deadline)`：登记消息。`msg` 非空且未登记过；`rcpts`
  为 1..32 个互异非空收件人；`deadline` 0..10^12。
- `Receipt(msg, rcpt, kind, attempt, ts)`：处置 sent/delivered/read/soft/hard
  回执（attempt 1..16，ts 0..10^12），返回 `Applied` / `Stale` /
  `Duplicate` / `Ignored`。
- `Tick(now)`：单调推进时钟；把 `deadline <= now` 且仍未决（fail 为无、
  r<2）的收件人置 `exp`，返回本次新置名单（按 `(msg, rcpt)` 字节序）。
- `Status(msg)`：返回汇总 `failed`/`inflight`/`partial`/`read`/`delivered`
  与每个收件人的 `(r, fail, late)`。

### 处置次序

Receipt 先做参数/消息/收件人校验（被拒不改任何状态，包括 seen 与时钟），
然后：

1. `(kind, attempt)` 已在 seen 中：`Duplicate`，不改任何状态。
2. 否则先加入 seen，再分支处置。这保证每个 `(kind, attempt)` 对同一收件人
   至多产生一次非 Duplicate 结果，各结果计数之和等于被接受的回执总数。

进展回执 sent/delivered/read（进展值 1/2/3）：

- fail 为 soft/hard：`Ignored`（终态不可变，r 也不再变化）。
- fail 为 exp：仅 delivered/read 且 `ts < deadline`（严格小于）才撤销到期，
  fail 置无并 `r = max(r, 进展值)`，`Applied`；否则 `Ignored`。sent 永不撤销。
- fail 为无：sent 先刷新 `sentA = max(sentA, attempt)`（即使回执晚于
  delivered 到达、结果为 Stale，sentA 仍推进）；随后 r 增大则 `Applied`，
  否则 `Stale`。

soft：fail 非无即 `Ignored`（exp 下也不撤销）；`r >= 2` 为 `Stale` 且不
记录；否则把 attempt 记入 softSet。

hard：fail 为 soft/hard 时 `Ignored`；`r >= 2` 时只置 late 标记、`Stale`
（已送达后的硬退信迟到）；否则置 fail=hard（含由 exp 升级），`Applied`。

### 软退信作废规则

每次记录 soft 后，`cnt = |{a in softSet : a >= sentA}|`：尝试号早于最近一次
已见 sent 尝试号的软退信视为已作废，不计入 cnt。`cnt >= S` 时 fail 置
soft，此后不可撤销；否则保持未决，回执结果仍是 `Applied`。

### 到期与可撤销性

Tick 只把 fail 为无且 r<2 的收件人置 exp。exp 不是终态：delivered/read
携带严格早于 deadline 的 ts 可撤销（同一回执按 `(kind, attempt)` 去重）；
hard 可把 exp 升级为 hard；soft 与 sent 不能撤销。撤销后若再次越过
deadline 且仍无进展，后续 Tick 会再次将其置 exp 并返回名单。

### 汇总口径

令 N 为收件人数、F 为 fail 非无人数、pending 为 fail 为无且 r<2 人数：

- `F == N`：`failed`
- 否则 `pending > 0`：`inflight`
- 否则（全部已决）`F > 0`：`partial`
- 否则 F=0：全体 r==3 为 `read`，否则为 `delivered`

### 拒绝原因优先级

只报第一个原因：

- Send：参数非法 → 消息已存在。
- Receipt：参数非法（kind/attempt/ts 越界）→ 消息不存在 → 收件人不属于消息。
- Tick：参数非法（now 越界）→ 时钟回退。
- Status：参数非法（msg 为空）→ 消息不存在。

### 本地验证

```bash
# 全量测试 + 竞态检测（含 2000 组朴素模型对拍）
go test -race -v ./...

# 仅跑对拍并查看输入/输出/判定依据日志（receipt/fuzz_trace.log）
go test -run TestDifferential2000 -v ./receipt

# 跳过耗时对拍的快速检查
go test -short ./...
```

`receipt/tracker_test.go` 覆盖题目全部边界场景（作废与不作废、cnt 恰为 S
与差 1、乱序 sent、hard 在 r=1/2 的差别、deadline 边界撤销、exp 升级、
五种汇总、被拒不改状态）；`receipt/diff_test.go` 内的朴素模型独立按规则
逐条重写，对 2000 组随机回执序列逐步比对结果与完整内部状态，样本轨迹写入
`receipt/fuzz_trace.log`；`receipt/concurrency_test.go` 验证并发不变量
（每个 `(kind, attempt)` 恰一次非 Duplicate）与重放确定性。
