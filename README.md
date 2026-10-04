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

## 步进扩缩容控制器（`scaler` 包）

`scaler` 实现带预热在途容量、冷却内追加差额与缩容阻止的步进扩缩容控制器。
构造用 `scaler.New(Config)`，任何约束不满足即以 `ErrInvalidConfig` 整体拒绝；
每次评估调用 `Evaluate(now, metric)`，全部方法可并发调用，结果等价于某个串行顺序。

### 有效容量与在途批次

- 状态含就绪容量 `cap` 与在途批次列表 `(readyAt, count)`。
- 有效容量 `eff = cap + 全部在途数量`，任何时刻 `Mn <= cap <= Mx` 且 `eff <= Mx`。
- 每次 `Evaluate` 先把 `readyAt <= now` 的批次并入 `cap`（恰等即就绪）；
  处理完后每个在途批次的 `readyAt` 都大于已接受的最大 `now`。
- 扩容不立即增加 `cap`，而是追加批次 `(now+W, target-eff)`，预热期 `W` 后就绪。

### 取整方向

- 扩容：`delta = max(ms, ceil(base*pct/100))`，向上取整并受最小步长 `ms` 兜底。
- 缩容：`delta = max(1, floor(cap*pct/100))`，向下取整并保证至少为 1，
  目标再经下限钳制 `target = max(Mn, cap-delta)`。

### 冷却内按 B0 追加差额

- 非冷却扩容（`lastOut` 为无或 `now >= lastOut+Cout`，恰等即结束）：
  `base = eff`，目标 `target = min(Mx, base+delta)`；`target > eff` 时追加差额批次，
  并记录 `B0 = eff`、`lastOut = now`；否则无动作且不更新 `lastOut`。
- 冷却内（`now < lastOut+Cout`）：`base = B0`（最近一次非冷却扩容前的有效容量），
  目标同样为 `min(Mx, B0+delta)`；仅当 `target > eff` 时追加差额 `target-eff`，
  `B0` 与 `lastOut` 均不变，因此冷却内只“补差额”，不会重复扩大目标。
- 同一 `metric` 在同一 `now` 重复评估，第二次起无动作（幂等去重）。

### 缩容阻止条件

`metric < Lw` 时按缩容档表取 `pct`，但满足以下任一条件即无动作：

1. 存在任何在途批次；
2. 处于缩容冷却内（`lastIn` 非无且 `now < lastIn+Cin`，恰等即结束）。

两个冷却相互独立：`lastOut/Cout` 只管扩容，`lastIn/Cin` 只管缩容。

### 拒绝语义

`Evaluate` 的拒绝原因可区分，按顺序只报第一个：
`ErrInvalidArgument`（`now<0` 或 `now>1e15`，`metric<0` 或 `metric>1e9`），
`ErrClockRegression`（`now` 小于已接受的最大 `now`）。
被拒绝的调用不改变任何状态；无动作的调用仍属被接受，会并入就绪批次并推进最大 `now`。

### 本地验证

```bash
# 单元测试（示例序列 + 全部边界用例）
go test ./scaler/

# 2000 组随机序列与朴素模拟对照，-v 打印每步输入、输出与判定依据
go test -v -run TestRandomAgainstNaive ./scaler/

# 并发与竞态验证
go test -race ./scaler/
```
