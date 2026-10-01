# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 阶梯费率计费器（`fee` 包）

按月累计成交额的超额累进阶梯费率计费器，支持成交登记、撤销重算、
费用封顶与跨账期比例结转。所有方法并发安全（内部以互斥锁串行化，
结果等价于某个串行顺序），相同操作序列重放得到完全相同的结果。

### 构造参数

`NewCalculator(thresholds, rates []int64, cap, rho int64)`：

- `thresholds`：严格递增的阈值 `t1 < … < tm`（`m ≥ 1`，`1 ≤ t1`，`tm ≤ 10^14`）。
- `rates`：`m+1` 个万分比整数费率 `r0 … rm`（`0 ~ 10000`）。
- `cap`：累计费用封顶 `CAP`（`0 ~ 10^14`）。
- `rho`：账期结转比例 `ρ`（万分比整数，`0 ~ 10000`）。

参数不满足要求时整体拒绝并返回 `ErrInvalidParam`。

### 分段累进公式与取整位置

令 `t0 = 0`，段 `k` 为 `[t_k, t_{k+1})`（最后一段无上限），段 `k` 费率为 `r_k`。
对累计成交额 `C`：

```
seg_k(C) = clamp(C − t_k, 0, t_{k+1} − t_k)   （最后一段为 max(C − t_m, 0)）
F(C)     = Σ_k floor(seg_k(C) × r_k / 10000)   （每段分别向下取整后再求和）
Φ(C)     = min(F(C), CAP)                      （封顶作用在累计费用上）
```

第 `i` 笔有效成交的累计额 `C_i = C0 + 前 i 笔有效成交金额之和`，
费用 `fee_i = Φ(C_i) − Φ(C_{i−1})`。由于封顶作用于累计费用 `Φ`
而非单笔费用，触顶后单笔费用可能小于未封顶差额，且继续成交费用为 0。
任意时刻账户当前账期有效成交费用之和恒等于 `Φ(C0 + 有效金额之和) − Φ(C0)`。

### 成交登记（`Trade`）

`Trade(acct, tid string, amt int64) (int64, error)` 登记成交并返回费用。
`tid` 全局唯一（跨账户与账期，被撤销的也占用）。错误按如下优先级只报第一个：

1. `ErrInvalidParam`：账户名或 `tid` 为空串、`amt` 不在 `1 ~ 10^9`；
2. `ErrDuplicateTID`：`tid` 已存在；
3. `ErrCumulativeLimit`：登记后账户当前账期累计额（含 `C0`）超过 `10^14`。

被拒绝的操作不改变任何状态。

### 撤销与重算（`Cancel`）

`Cancel(tid string) (oldFee int64, changes []FeeChange, error)` 只能撤销
当前账期内仍有效的成交。撤销后该账户当前账期排在被撤销成交之后的有效成交
按新的累计额从起点 `C0` 重算费用，结果与把剩余有效成交按原次序从 `C0`
重新登记逐笔相同；`C0` 不因撤销而改变。返回被撤销成交的旧费用与费用发生
变化的成交列表（按登记次序，费用未变的不列出）。错误按如下优先级：

1. `ErrTradeNotFound`：成交不存在；
2. `ErrAlreadyCancelled`：成交已撤销；
3. `ErrPeriodClosed`：成交不属于当前账期（账期已结）。

### 账期结转（`NextPeriod`）

`NextPeriod()` 使账期加 1，对全部已出现账户原子地执行：

```
C0 ← floor((原 C0 + 本账期有效成交金额之和) × ρ / 10000)
```

没有任何成交的账户也按原 `C0` 结转（逐期复合，如连续两期得
`floor(floor(C×ρ/10000)×ρ/10000)`）。结转后此前的成交永远不可再撤销。

### 查询

- `Period()`：当前账期编号（从 0 开始）。
- `C0(acct)`：账户当前账期起点累计额。
- `Trades(acct)`：账户当前账期全部成交快照（含已撤销，按登记次序）。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作序列与朴素重算模型的逐步对照）
go test ./fee/

# 打印随机对照日志（输入、输出与判定依据）
go test ./fee/ -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./fee/
```

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
