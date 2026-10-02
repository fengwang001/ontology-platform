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

## O(1) 风格单 CPU 调度器（`scheduler` 包）

`scheduler` 包实现了一个确定性、可并发调用的单 CPU 调度器，语义与
Linux O(1) 调度器同构：活动/过期两组按优先级分桶的 FIFO 队列 +
优先级位图选择。

### 时间片与动态优先级

- 静态优先级 `sp = 120 + nice`（`nice ∈ [-20, 19]`）。
- 时间片：`sp < 120` 时 `ts(sp) = (140 - sp) × 20`，否则
  `ts(sp) = max(5, (140 - sp) × 5)`（`sp=100 → 800`，`sp=120 → 100`，
  `sp=139 → 5`）。
- 睡眠量 `s ∈ [0, 1000]`：运行每 tick 减 1（不低于 0），`Wake` 时
  增加 `now - sleep_start`（封顶 1000）。
- 奖励 `bonus(s) = floor(s / 100)`（0..10），动态优先级
  `prio = min(139, max(100, sp - bonus + 5))`，数值越小越优先；
  `bonus >= 7` 为交互任务。

### 两组队列、互换与饥饿保护

- 活动与过期两组各 40 个 FIFO 队列（prio 100..139）。`dispatch` 取
  活动组最小 prio 非空队列的队首；活动组空而过期组非空时两组互换
  并把 `expired_ts` 置 0；两组皆空则空闲。
- 时间片到期时重算 `prio`、重置 `ts_left`：交互任务且未饥饿时回活
  动组队尾，否则进过期组队尾；过期组追加前为空才把 `expired_ts`
  置为 `now`。
- 饥饿判定（先判定、后入队）：过期组非空且
  `now - expired_ts >= 100 × nr`（`nr` 为含当前任务的非睡眠任务
  数）时 `starving` 为真，交互任务也进过期组。

### 抢占与睡眠记账

- `arrive`（Spawn/Wake/Fork 子任务）把任务追加到活动组队尾；`cur`
  为空直接 `dispatch`；新任务 `prio` 严格小于 `cur.prio` 时抢占，
  被抢占者回到活动组其 prio 队首并保留剩余时间片，相等不抢占。
- `Sleep` 记录 `sleep_start = now`，保留 `prio` 与 `ts_left`；
  `Wake` 按新 `s` 重算 `prio` 后 `arrive`。

### Fork 的时间片切分

只有当前运行任务可派生。设父剩余时间片为 `t`：子任务
`ts_left = ceil(t/2)`、`s = floor(父 s / 2)`、按子 `s` 重算 `prio`；
父 `ts_left = floor(t/2)`。`t == 1` 时父立即按时间片到期流程处理
（重算 `prio`、重置 `ts_left`、先判 `starving` 再入队、重新
`dispatch`，但不推进 `now`、不改 `s`，且 `nr` 不含尚未入队的子任
务），随后子任务才 `arrive`。

### 位图选择复杂度

每组队列配一个两级优先级位图（1 个汇总字 + 每组 1 个 64 位字）。
`dispatch` 选队列每次至多考察 3 个位图字（活动组汇总字 1 次，互
换后汇总字 + 组字各 1 次），与任务数无关；实测 10^4 个排队任务时
每次 `Tick`/`Spawn`/`Sleep`/`Wake`/`Fork` 的非导出计数器 `words`
不超过 4（见 `TestWordsCounterBound`）。

### 并发与确定性

所有操作与查询由一把互斥锁串行化，并发调用等价于某个串行顺序；
不变量（每个未睡眠任务恰在 `cur` 或某队列出现一次、`s ∈ [0,1000]`、
`expired_ts != 0` 当且仅当过期组非空等）在测试中持续校验。相同时
钟与操作序列重放得到完全相同的每 tick 运行序列。

### 本地验证

```bash
# 全部单元测试（公式、边界、错误顺序、规格示例逐 tick 回放）
go test ./scheduler/

# 2000 组随机操作序列与朴素模拟（线性扫描 40 队列）逐步对照，
# 日志打印输入、输出与判定依据
go test ./scheduler/ -run TestFuzzAgainstNaive -v

# 竞态检测 + 并发线性化冒烟
go test -race ./scheduler/
```
