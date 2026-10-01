# plancache：预备语句计划缓存选择器

`plancache.Selector` 按已观察到的执行次数与代价，在**定制计划**
（每次按绑定参数重新规划）与**通用计划**（规划一次、多组参数复用）之间
做可复现的选择，灵感来自 PostgreSQL 的 custom/generic plan 选择机制。

## 构造

```go
s, err := plancache.New(k, p)
```

- `k`：强制试用定制计划的次数，必须 `>= 1`，否则返回 `ErrKTooSmall`。
- `p`：每次定制规划的固定开销，必须 `>= 0`，否则返回 `ErrPNegative`
  （当 `k` 也非法时先报 `ErrKTooSmall`）。
- 架构版本初始为 `0`。

## 语句登记

- `Prepare(name)`：在当前架构版本登记语句；名字为空报 `ErrEmptyName`，
  已存在报 `ErrNameExists`。
- `Drop(name)`：删除语句；不存在报 `ErrStatementNotFound`。
- 每条语句持有：
  - `c`：自上次版本重置以来定制计划执行次数（初始 `0`）。
  - `sum`：定制计划代价总和（初始 `0`）。
  - `g`：通用计划代价（初始未知，记为 `nil`）。
  - 待决标记（初始无）。

## 决定规则与统计口径

`Next(name)` 返回三种决定之一并设置待决标记。规则按序判定：

1. `c < K` → `Custom`（继续试用定制计划）。
2. 否则 `g` 未知 → `BuildGeneric`（构建一次通用计划）。
3. 否则当 `g*c < sum + P*c`（**严格小于**）→ `UseGeneric`。
4. 不满足严格小于（含相等）→ `Custom`。

所有运算均使用 `math/big.Int` 的精确整数运算：代价上限为 `2^40`、
`c` 可远超 `2^20`，`g*c` 与 `sum + P*c` 都不会溢出。

## 协议配对（待决标记）

同一时刻每条语句至多有一个待决决定；`Next` 在有待决时返回
`ErrPendingExists`。合法配对如下：

- `Custom` 待决 → `ReportCustom(name, cost)`：`c += 1`、`sum += cost`，
  清除待决。
- `BuildGeneric` 待决 → `ReportGeneric(name, cost)`：`g = cost`，
  清除待决。
- `UseGeneric` 待决 → `DoneGeneric(name)`：仅清除待决，**不改任何统计**。

`cost` 必须在 `[0, 2^40]`（闭区间，`2^40` 合法），越界返回
`ErrCostOutOfRange`。无待决或待决种类不符返回 `ErrPendingMismatch`。
任何被拒绝的操作都不改变状态；尤其 `cost` 非法时待决标记原样保留，
可立即用合法代价重新上报。

统计含义：`g*c` 是已执行 `c` 次的通用计划累计代价（不含额外规划开销），
`sum + P*c` 是定制计划的执行代价总和加每次固定规划开销。严格小于才切换，
因此代价相等时坚持定制计划，选择序列对相同输入完全确定。

## 架构版本重置

`Bump(v)` 要求 `v` 严格大于当前版本，否则返回 `ErrVersionNotGreater`。
升级时立即对所有**登记版本小于 `v`** 的语句执行：`c`、`sum` 清零，
`g` 置为未知，清除待决，并把登记版本改为 `v`（随后从头试用 `K` 次）。
已经登记在 `v` 版本的语句不受影响；当前架构版本随后更新为 `v`。

## 错误检查顺序

各方法按规格所列顺序只返回第一个可区分的原因（见 `errors.go`）：

- `New`：`ErrKTooSmall` → `ErrPNegative`
- `Prepare`：`ErrEmptyName` → `ErrNameExists`
- `Next`：`ErrStatementNotFound` → `ErrPendingExists`
- `ReportCustom`/`ReportGeneric`：`ErrStatementNotFound` →
  `ErrPendingMismatch` → `ErrCostOutOfRange`
- `DoneGeneric`：`ErrStatementNotFound` → `ErrPendingMismatch`
- `Bump`：`ErrVersionNotGreater`
- `Drop`：`ErrStatementNotFound`

## 并发

- 不同语句上的操作可并行（注册表用 `sync.RWMutex` 读写分离）。
- 同一语句上的操作用语句级互斥串行化：并发 `Next` 恰有一个成功，
  其余得到 `ErrPendingExists`；整体结果等价于某个串行顺序。
- 相同的操作序列重放得到完全相同的决定序列与错误序列。

## 本地验证

```bash
# 常规测试（含 2000 组随机序列对拍）
go test ./plancache

# 竞态检测 + 详细日志（打印种子 1/500/1000/2000 的输入、输出与判定依据）
go test -race -v ./plancache

# 跳过较长的对拍
go test -short ./plancache

go vet ./...
gofmt -l .
```

对拍方式：`fuzz_test.go` 中独立实现一份以 `big.Int` 按上述规则书写的
朴素模型，对 2000 个固定种子（`1..2000`）生成的随机操作序列逐操作比较
错误与决定，并用同一序列重放验证确定性。
