# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 登录失败限制器（`ratelimit` 包）

`ratelimit` 包按**账号**与**来源（如 IP）**两个相互独立的维度统计登录
失败，并对暴力尝试施加渐进式、有期限的锁定。代码见 `ratelimit/ratelimit.go`。

### 参数与每键状态

`New(Config{...})` 接收五个参数，任一非正或 `MaxLock < BaseLock` 时构造失败：

- `Window`（W）：失败计数的滑动窗口。
- `Threshold`（K）：窗口内触发锁定所需的失败次数。
- `BaseLock`（B）：一级锁定的时长。
- `MaxLock`（M）：单次锁定时长的上限。
- `Cooldown`（R）：上次锁定截止后，级别归零所需的静默时长。

每个账号键 / 来源键各自记录：窗口内的失败时刻列表、锁定截止时刻
`lockedUntil`、当前渐进级别 `j`。

### 失败记录与触发规则

`Attempt(account, source, passwordCorrect, now)` 先做入参校验（顺序固定，
只报第一个原因，且被拒绝的尝试不改变任何键状态）：账号为空 →
`empty_account`；来源为空 → `empty_source`；`now` 早于已见最大时钟读数 →
`clock_rewound`。

校验通过后按下列顺序判定：

1. **锁定优先**：账号键或来源键任一满足 `now < lockedUntil`，直接拒绝为
   `locked`，不计失败、不延长锁定；口令是否正确都不放行。
2. **口令正确**：清空账号键的窗口失败记录并将级别归零；**来源键保持不动**。
3. **口令错误**：两个键各追加一次失败（先剔除 `t <= now-W` 的旧时刻，
   仅保留严格晚于 `now-W` 的记录）。任一键窗口内失败数达到 K 即触发锁定，
   触发后清空该键失败记录。触发锁定的这次尝试本身仍只报“口令错误”，
   不会被当作锁定拒绝。

### 渐进锁定与冷却

- 触发时若 `now >= 该键上次 lockedUntil + R`，先把级别 `j` 归零。
- 随后 `j = j + 1`，锁定到 `now + min(B * 2^(j-1), M)`，即时长依次为
  B、2B、4B……并在 M 处封顶（翻倍过程带溢出保护）。
- 锁定截止时刻始终是有限值，合法用户最多等待当前级别时长即可重试；
  锁定过期且再经历冷却 R 后级别重新从 1 开始，不会被无限期锁住。

### 解锁时刻与锁定维度的取法

锁定拒绝结果 `Result{Locked: true}` 中：

- `UnlockAt` 取当前所有处于锁定状态的键的 `lockedUntil` 中**较晚的一个**
  （边界为严格小于：`now == lockedUntil` 时视为已解锁）。
- `Dimensions` 列出处于锁定的维度，账号在前、来源在后，便于区分是账号
  被打、来源喷洒还是两者同时锁定。

### 并发与确定性

所有键状态由一把互斥锁保护，`Attempt` 可并发调用：并发失败不会丢失，
每次锁定恰由窗口内第 K 次失败引起且级别只加一；锁定期内的并发尝试全部
被拒且不修改状态。除被拒绝的尝试外，相同的（账号、来源、口令判定、时钟）
序列产生相同的结果序列。

### 日志

每次尝试都打印一行结构化日志，包含输入（账号、来源、口令判定、时刻）、
输出（放行/锁定/拒绝、原因、解锁时刻、锁定维度）与判定依据；可通过
`SetLogger` 注入自定义 `Logger`（传 `nil` 恢复默认 stderr 日志）。

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

# 只跑登录失败限制器（带竞态检测与详细日志）
go test -race -v ./ratelimit

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
