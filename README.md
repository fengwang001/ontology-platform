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

## 步进扩缩容控制器（`autoscaler` 包）

`autoscaler` 实现带预热在途容量、冷却内追加差额与缩容阻止的步进扩缩容
控制器。通过 `NewScaler(cfg)` 构造（配置非法时整体拒绝，返回
`ErrInvalidConfig`），通过 `Evaluate(now, metric)` 驱动决策，通过
`Snapshot()` 查询一致性快照。所有方法可并发调用，结果等价于某个串行顺序。

### 有效容量与在途批次

- 就绪容量 `cap` 之外，扩容以在途批次 `(readyAt, count)` 的形式预热，
  `readyAt = 触发时刻 now + W`。
- 有效容量 `eff = cap + 全部在途数量`；每次 `Evaluate` 先把 `readyAt <= now`
  （恰等即就绪）的批次并入 `cap`。
- 任意时刻保证 `Mn <= cap <= Mx`、`cap + 在途总数 <= Mx`，且处理完后每个
  在途批次的 `readyAt` 都大于已接受的最大 `now`。

### 扩容与缩容的取整方向

- 扩容向上取整：`delta = max(ms, ceil(base * pct / 100))`，
  `target = min(Mx, base + delta)`，只追加差额 `target - eff` 为一个新批次。
- 缩容向下取整：`delta = max(1, floor(cap * pct / 100))`，
  `target = max(Mn, cap - delta)`，立即生效并记录 `lastIn`。
- 档表按 `lo <= v < 下一档 lo` 选档（最后一档无上界），扩容 `v = metric - H`，
  缩容 `u = Lw - metric`；`metric == H` 触发扩容，`metric == Lw` 不触发缩容。

### 冷却内按 B0 追加差额

- 非冷却扩容（`lastOut` 为无或 `now >= lastOut + Cout`，恰等即冷却结束）以
  当前 `eff` 为 base，成功追加后记录 `B0 = eff`、`lastOut = now`。
- 冷却内（`now < lastOut + Cout`）以 `B0` 为 base 计算
  `target = min(Mx, B0 + delta)`，仅在 `target > eff` 时追加差额批次，
  `B0` 与 `lastOut` 不变；若 `target <= eff`（含上限钳制后相等）则无动作且
  不更新 `lastOut`。
- 缩容冷却 `Cin` 与扩容冷却 `Cout` 相互独立。

### 缩容阻止条件

- 存在任何在途批次时缩容直接无动作；批次就绪并入后才允许缩容。
- `lastIn` 非无且 `now < lastIn + Cin` 时缩容无动作。

### 拒绝与幂等

- `Evaluate` 拒绝原因可区分且只报第一个：`ErrInvalidParam`（`now` 越界
  `[0, 10^15]` 或 `metric` 越界 `[0, 10^9]`）优先于 `ErrClockRegression`
  （`now` 小于已接受的最大 `now`）。被拒绝的操作不改变任何状态。
- 无动作的 `Evaluate` 属于被接受：仍会并入就绪批次并推进最大 `now`。
- 同一 `metric` 在同一 `now` 重复评估，自第二次起无动作（幂等保护），
  因此相同序列重放得到完全相同的动作、容量与批次。

### 本地验证

```bash
# 全部单测（含题目示例追踪、边界场景、并发与 2000 组随机序列对照朴素模拟）
go test ./autoscaler/

# 竞态检测 + 打印随机对照的输入/输出/判定依据日志
go test -race -v -run TestRandomAgainstNaiveSim ./autoscaler/
```
