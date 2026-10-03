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

## 步进扩缩容控制器（autoscaler 包）

`autoscaler` 包实现带预热在途容量、冷却内追加差额与缩容阻止的步进
扩缩容控制器。构造参数非法时 `New` 整体拒绝并返回 `ErrInvalidConfig`；
`Evaluate` 的拒绝原因可区分且只报第一个：参数非法
（`ErrInvalidParam`）优先于时钟回退（`ErrClockBackward`）。被拒绝的
操作不改变任何状态；无动作的评估仍并入就绪批次并推进最大 `now`。
所有方法可并发调用，结果等价于某个串行顺序。

### 有效容量与在途批次

- 就绪容量 `cap` 与在途批次列表 `（就绪时刻, 数量）` 共同决定有效容量
  `eff = cap + 全部在途数量`。
- 每次评估第一步把就绪时刻不大于 `now` 的批次并入 `cap`（恰等即就绪），
  处理完后每个在途批次的就绪时刻都大于已接受的最大 `now`。
- 任何时刻 `Mn <= cap <= Mx`，且 `cap + 在途总数 <= Mx`。

### 取整方向

- 扩容：`delta = max(ms, ceil(base * pct / 100))`，向上取整后再与最小
  步长 `ms` 取大。
- 缩容：`delta = max(1, floor(cap * pct / 100))`，向下取整后至少为 1。

### 冷却内按 B0 追加差额

- 非冷却扩容（`lastOut` 为无或 `now >= lastOut + Cout`）：以当前 `eff`
  为 base 计算 `target = min(Mx, base + delta)`；当 `target > eff` 时追加
  批次 `(now + W, target - eff)`，并令 `B0 = eff`、`lastOut = now`，
  否则无动作且不更新 `lastOut`。
- 冷却内（`now < lastOut + Cout`）：base 取 `B0` 而非当前 `eff`，
  `target = min(Mx, B0 + delta)`；仅当 `target > eff` 时追加差额
  `target - eff`，`B0` 与 `lastOut` 均不变。
- 同一 `metric` 在同一 `now` 重复评估时，第二次起无动作。

### 缩容阻止条件

存在任一在途批次时缩容不动作；否则若处于缩容冷却内
（`now < lastIn + Cin`）也不动作。缩容冷却独立于扩容冷却：
扩容冷却不阻止缩容，缩容冷却也不阻止扩容。

### 本地验证方法

```bash
# 全量测试（含 2000 组随机序列与朴素模拟对照）
go test ./...

# 查看随机对照的输入、输出与判定依据日志
go test ./autoscaler/ -run TestRandomSequencesAgainstNaiveSim -v

# 竞态检测
go test -race ./...
```
