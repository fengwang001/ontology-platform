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

## 预算花费守卫（`budget` 包）

`budget.Guard` 实现带阶梯告警、线性外推预测与超支冻结的预算花费守卫。
所有方法可并发调用，效果等价于某个串行顺序；相同操作序列重放得到完全
相同的事件序列与状态。

### 构造

```go
g, err := budget.NewGuard(D, p, F, Dmin, B)
```

- `D`：周期天数，1 到 366。
- `p`：阶梯百分比列表，1 到 8 个严格递增的整数，每个 1 到 1000。
- `F`：预测触发百分比，1 到 1000。
- `Dmin`：预测所需最小已过天数，1 到 D。
- `B`：初始预算（单位分），1 到 10^15。

任一参数不满足约束时整体拒绝并返回 `budget.ErrInvalidConfig`。

### 阶梯告警（一次性触发与重新武装）

每次评估按 `p` 升序检查每个阶梯：未触发且满足 `S*100 >= B*p`（恰等即
触发）时置已触发并产生 `LADDER(p)` 事件。阶梯标志只由评估置真，只会
被 `AdjustBudget` 的重新武装清除：预算调整为 `B2` 时，已触发且满足
`S*100 < B2*p` 的阶梯被清除（恰等保持），随后以新预算重新评估，可能
当场补报新越线的阶梯。退款本身从不重新武装，因此退款后再次越线不会
重复告警。

### 预测告警

令 `e = cur+1`（已过天数，含当日）。当 `e >= Dmin` 时计算线性外推月末
花费 `proj = floor(S*D/e)`（向下取整），条件为 `proj*100 >= B*F`（恰等
即触发）；`e < Dmin` 时条件为假。条件为真且预测标志 `f` 为假时置 `f`
并产生 `FORECAST` 事件；条件为假时清除 `f`（无事件）；`f` 已为真且条
件持续为真时不重复产生事件。`proj*100` 可超出 int64，比较使用 128 位
运算。

### 冻结与拒绝优先级

冻结是纯函数：`S >= B` 即冻结，`Frozen()` 返回该值。评估结束时冻结状
态由假变真产生 `FROZE`、由真变假产生 `THAWED`（排在 `LADDER`、
`FORECAST` 之后），两者严格交替且以 `FROZE` 开始。冻结时正数花费被
拒绝，零或负数花费（退款）仍被接受。

`Spend(day, x)` 的拒绝原因可区分，按以下顺序只报第一个：

1. `RejectInvalidParam`：`day` 不在 `[0, D)` 或 `x` 不在 `[-10^12, 10^12]`。
2. `RejectDayRegression`：`day < cur`（日期回退）。
3. `RejectFrozen`：已冻结且 `x > 0`。
4. `RejectOutOfRange`：`S+x` 小于 0 或大于 10^15。

`AdjustBudget(B2)` 只可能因 `B2` 越界而以 `RejectInvalidParam` 拒绝。
被拒绝的操作不改变 `S`、`cur`、`B`、阶梯标志与 `f`。

### 本地验证

```bash
# 单元测试（边界、事件次序、冻结/解冻、重新武装等）
go test ./budget

# 含 2000 组随机序列与朴素大整数模拟对照，-v 打印每步输入/输出/判定依据
go test ./budget -run TestRandomAgainstNaiveSimulation -v

# 竞态检测
go test -race ./budget
```
