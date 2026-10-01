# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 实例流量份额计算器

`shares.go` 中的 `ontology.Calculator` 实现带**慢启动爬坡**与**单实例份额上限**的
固定份数分配器，全部操作互斥、可并发调用，结果等价于某个串行顺序，重放同一操作
序列得到完全相同的份额序列。

### 构造与操作

- `New(Wslow, T, Y)`：`Wslow`∈[1,10⁹] 为慢启动窗口；`T`∈[1,10⁶] 为总份数；
  `Y`∈[1,T] 为单实例份数上限。任一越界返回 `ErrInvalidConfig`，整体拒绝。
- `AddHost(id, weight, now)`：登记实例，`weight`∈[1,10⁶]，登记后健康，
  上线时刻 `join=now`。
- `SetWeight(id, weight, now)`：只改权重，保留 `join`。
- `SetHealth(id, healthy, now)`：仅在 不健康→健康 时把 `join` 重置为 `now`，
  即恢复后从头爬坡；设成与当前相同的健康位不改变任何状态。
- `RemoveHost(id, now)`：移除实例；同一 `id` 再次登记视为全新实例。
- `Shares(now)`：返回 `[]Share`，覆盖全部实例（含不健康实例，份额为 0），
  按 `id` 字节序升序排列。

时间为 int64，合法范围 `[0, 10¹⁵]`；每个被接受的操作（含报错的 `Shares`）都会
推进已见最大时刻，之后更小的 `now` 报 `ErrClockRollback`。

错误按固定顺序只报第一个：参数非法 → 时间非法 → 时钟回退 → 状态非法
（`Shares` 在容量不足之前先报 `ErrNoHealthyHost`）。被拒绝的操作不改变任何状态。

### 有效权重公式

设 `e = now − join`：

- 不健康：`eff = 0`，份额恒为 0；
- 健康且 `e ≥ Wslow`：`eff = weight`（爬坡结束）；
- 健康且 `e < Wslow`：`eff = max(1, ⌊weight × e / Wslow⌋)`。

因此 `e = 0` 或下取整为 0 时有效权重仍至少为 1（健康实例可被分到 0 份，但其参与
权重和与余数计算）。

### 最大余数法

每轮对当前活动集合 `A` 分配剩余份数 `Trem`，设有效权重和为 `E = Σ eff`：

1. 每实例先取底数 `⌊eff × Trem / E⌋`；
2. 余数 `r = (eff × Trem) mod E`；
3. 未分完的差额份数（少于实例数）按余数从大到小各加 1 份；余数并列时 `id`
   字节序小者优先。

### 分轮固定与再分配

- 若本轮有实例份数 `> Y`，把**所有**超限实例在同一轮一次性固定为 `Y`，从 `A`
  移除，`Trem -= Y × 固定数`，用剩余实例进入下一轮；
- 份数恰等于 `Y` 不触顶；下一轮的底数与余数全部基于新的 `Trem` 和剩余实例的
  有效权重重新计算；
- 没有超限实例时本轮结果即最终结果；
- 每轮至少固定一个实例，轮数不超过健康实例数；
- 当 `A` 已空而 `Trem > 0`（即 `Y × 健康实例数 < T`）时报
  `ErrInsufficientCapacity`。

成功时各份额非负、总和恒为 `T`、每个实例不超过 `Y`、不健康实例为 0。

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

份额计算器的专项验证（`go test -v` 可见详细判定日志）：

```bash
# 全部专项用例 + 2000 组随机操作序列与朴素参考实现对照
go test -v -run 'TestInvalidConfig|TestSpecExamples|TestCapping|TestSlowStart|TestHealth|TestRemainder|TestValidation|TestInvariants|TestConcurrent|TestRandomDifferential'

# 竞态检测
go test -race ./...

# 跳过 2000 组随机对照的快速模式
go test -short ./...
```

随机对照测试把生成的每一步输入（含非法参数/非法时间/时钟回退）、输出与错误、
成功时的份额及总和判定依据打印到测试日志；朴素参考实现按规则逐轮抄写在
`shares_test.go` 中（`naiveCalc`），两侧的错误与份额逐项比对。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
