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

## 网卡式中断节流合并器（`coalesce` 包）

`coalesce` 实现带注入时钟的中断节流合并器：按最小触发间隔 `gap` 与累计事件数门限 `thr`
决定何时向 CPU 发出中断，并在屏蔽（mask）与确认（ack）约束下合并事件。

### 状态与节流规则

- 状态：待处理事件数 `p`（初始 0）、上次触发时刻 `last`（初始为「从未触发」，视为无限久以前）、
  等待确认标志 `w`、屏蔽标志 `m`（初始均为假）。
- 允许时刻 `allow = last + gap`；从未触发过时不构成限制。
- 「可触发」当且仅当 `p > 0`、`w` 为假、`m` 为假，并且（当前时刻 `>= allow` 或 `p >= thr`）。
- 触发产生一条记录 `(触发时刻, 携带数 = 当时的 p)`，随后 `p = 0`、`last = 触发时刻`、`w = 真`。

### 三步处理顺序

每个操作（`Event` / `Tick` / `Ack` / `Mask` / `Unmask`，均带整数时刻 `t`）依次执行：

1. **补触发**：若此前至少触发过一次、`p > 0`、`w` 与 `m` 都为假、`p < thr` 且 `allow <= t`，
   则在时刻 `allow`（不是 `t`）触发一次；从未触发过时跳过，由第三步在 `t` 处理。
2. **施加操作本身**：`Event` 令 `p += n`；`Ack` 令 `w = 假`；`Mask` 置 `m = 真`；
   `Unmask` 置 `m = 假`（重复 Mask/Unmask 有效且无其他影响）；`Tick` 无操作。
3. **在 `t` 判断是否可触发**，可则在 `t` 触发。

触发时刻的取法：补触发固定在 `allow`（即使 `t` 已大幅跳过 `allow`），下一个 `allow`
从该补触发时刻起算；第三步触发取操作自身的 `t`（因此 `Ack`/`Unmask` 时刻可立即触发）。

### 拒绝规则

- 构造时 `gap < 0`（`ErrNegativeGap`）或 `thr < 1`（`ErrBadThreshold`）。
- 操作时刻小于上一次成功操作的时刻（`ErrTimeRegression`，首个操作不受限）。
- `Event` 的 `n < 1`（`ErrBadEventCount`）；`Ack` 时按调用前状态 `w` 为假（`ErrNotWaitingAck`）。
- 时刻回退优先于其他原因；被拒绝的操作不改变任何状态，也不触发补触发。
- 所有原因均为可 `errors.Is` 区分的哨兵错误。

### 一致性与并发

所有操作与查询（`Records` / `Snapshot`）在互斥锁下串行化，并发调用等价于某个串行顺序。
任意时刻：全部中断携带数之和加 `p` 等于成功 `Event` 的 `n` 之和；触发次数减去成功 `Ack`
次数恒为 0 或 1；触发时刻非递减；相同操作序列重放得到完全相同的中断记录。

### 本地验证

```bash
# 场景用例 + 与逐步朴素模拟的随机对照（日志打印输入、输出与判定依据）
go test -v ./coalesce/

# 竞态检测
go test -race ./coalesce/
```
