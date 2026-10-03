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

## 分层调度预算管理器（`budget` 包）

`budget/` 实现周期资源模型（Periodic Resource Model）下的分层 EDF 预算管理：
为每个组件求最小周期预算 `MinBudget`，并在全局带宽约束下增量接纳任务，
预算、带宽与拒绝点均可精确复现。

### 最坏供给界 sbf

组件周期 `Π`、预算 `Θ`，令服务等待 `a = Π − Θ`：

```
q     = floor(max(0, t − a) / Π)
sbf(t) = q·Θ + max(0, t − 2a − q·Π)
```

- `Θ = Π` 时 `sbf(t) = t`（满供给）。
- 直观含义：服务时隙位于各周期的 `a + kΠ` 偏移处（半无限时间线）；
  `t ≤ a` 无供给，`a < t < 2a` 至多拿到部分首份服务，
  `t = 2a` 是首份完整预算服务可用的点，之后每 `Π` 给 `Θ`。
- 朴素验证：`Π ≤ 4` 时枚举每周期内 `Θ` 个时隙的全部放置方式
  （至多 `C(4,2)=6` 种）与窗口起点 `s ∈ [0, Π)`，对窗口 `[s, s+t)`
  计数取最小值，与闭式公式逐点相等（见 `TestSBFNaiveExhaustive`）。

### 任务需求界 dbf

任务 `(C, T, D)`（`1 ≤ C ≤ D ≤ T ≤ 1000`）在 EDF 下：

```
dbf_i(t) = max(0, floor((t − D) / T) + 1) · C   // t < D 时为 0
dbf(t)   = Σ_i dbf_i(t)
```

`t = D` 恰好计入第一份作业需求。

### 可调度条件与检查范围

任务集在预算 `Θ` 下可调度当且仅当：

1. `Σ C_i/T_i ≤ Θ/Π`，用 `math/big.Rat` 精确比较，全程无浮点；
2. 对所有 `0 < t ≤ Dmax + H` 有 `dbf(t) ≤ sbf(t)`，
   其中 `Dmax = max D_i`，`H = lcm(Π, T_1, …, T_n)`。

范围来由：`sbf(t) − dbf(t)` 在超区间之后以 `Θ/Π − ΣC/T ≥ 0` 的斜率增长，
故只须检查一个宏周期 `H` 加上最大截止期；dbf 只在各任务的
`D_i + mT_i` 处跳变，sbf 为分段线性，因此**只需检查 dbf 跳变点**
（多路归并生成，不逐个整数 t 扫描）。检查点数不超过
`Σ ⌈(Dmax+H)/T_i⌉`，由非导出计数器与断言保证
（`TestJumpPointBoundAndNoIntegerScan`、`TestSizeTierPointCounts`）。

`Dmax + H > 10^6` 判为“规模过大”（`too_large`），LCM 在乘法中途
超过阈值即停止，防止溢出。

### MinBudget 与二分

`sbf` 对 `Θ` 单调，故在 `[1, Π]` 上二分最小可行 `Θ`，先终检 `Θ = Π`；
可行性检查次数不超过 `⌈log₂ Π⌉ + 1`（非导出原子计数器
`feasibilityChecks` 在 `TestBinarySearchCheckCount` 中对
`Π = 1..1000` 断言）。`Θ = Π` 仍不可行时报 `infeasible` 并携带
最小违反点 `t`；若仅因 `Σ C/T > 1` 不可行则违反点记 `0`。

### 预算只增不减与全局带宽

- `Declare(name, Π)`：建空组件，`θ = 0`，最多 8 个组件。
- `AddTask`：`θ' = max(θ, MinBudget(Π, 当前任务 ∪ {新任务}))`；
  已分配预算只增不减（删除任务不会自动降预算）。
- `RemoveTask`：只删任务，`θ` 不变。
- `Compact(name)`：把 `θ` 重算为当前任务集的 `MinBudget`
  （空集为 0），释放带宽。
- 全局接纳条件：`Σ θ/Π ≤ 1`（**恰等于 1 通过**，超过一点拒绝），
  用 `big.Rat` 精确比较；`Total()` 返回既约分数（零为 `0/1`）。
- 每组件至多 8 个任务，任务编号仅组件内唯一。

拒绝原因按固定顺序只报第一个：参数非法 → 组件/任务不存在 →
名称或编号重复 → 容量已满 → 规模过大 → 不可行 → 过载。
被拒绝的操作不留下半更新状态（先在任务副本上计算，全部通过后提交）。
所有方法由单把 `sync.RWMutex` 串行化临界区，并发调用等价于某个串行顺序；
相同操作序列重放得到完全相同的 `θ`、`Total()` 与拒绝点。

### 本地验证

```bash
# 单元测试 + 2000 组随机操作序列对拍（带朴素穷举参考模型）
go test -v ./budget/

# 竞态检测
go test -race ./budget/

# 全量 / 格式化 / 静态检查
go test ./...
gofmt -l .
go vet ./...
```

随机对拍（`TestRandomDifferential2000`）中参考模型对 `Π ≤ 4` 的组件用
穷举时隙放置 + 逐个整数 `t` 扫描求 `MinBudget`，并对每个操作比较
接受/拒绝原因、拒绝点、各组件 `θ` 与 `Total()`；`-v` 日志打印每组序列的
输入操作、输出与判定依据。
