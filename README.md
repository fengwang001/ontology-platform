# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 超额累进阶梯计费器

`ontology.CumulativeBilling` 位于 `ontology/billing.go`，按账期内每个账户的有效成交累计成交额计算超额累进费用。

构造参数：

- `thresholds`：严格递增阈值 `t1 < t2 < … < tm`，`m >= 1`，`1 <= t1`，`tm <= 10^14`。
- `rates`：`m+1` 个万分比费率 `r0, …, rm`，范围 `0..10000`。
- `capFee`：累计费用封顶值 `CAP`，范围 `0..10^14`。
- `carryRate`：结转比例 `ρ`，万分比整数，范围 `0..10000`。

令 `t0 = 0`，段 `k` 为 `[t_k, t_{k+1})`，最后一段 `[t_m, +∞)`。对累计成交额 `C`，每一段独立计算：

```text
seg_k(C) = clamp(C-t_k, 0, t_{k+1}-t_k)
seg_m(C) = max(C-t_m, 0)
F(C)      = Σ floor(seg_k(C) * r_k / 10000)
Φ(C)      = min(F(C), CAP)
```

向下取整发生在每个分段乘以费率之后，而不是单笔成交整体计费后再取整；`CAP` 只作用于累计费用 `Φ(C)`。

账户账期起点为 `C0`，当前账期有效成交按登记次序排列。第 `i` 笔的累计额为：

```text
C_i = C0 + 前 i 笔仍有效成交金额之和
fee_i = Φ(C_i) - Φ(C_{i-1})
```

因此每笔费用非负，且当前账期有效费用之和恒等于：

```text
Φ(C0 + 当前有效成交金额之和) - Φ(C0)
```

`Trade(acct, tid, amount)` 登记成交并返回该笔费用。`tid` 必须是非空且全局唯一；即使成交已撤销或属于旧账期，编号仍继续占用。拒绝原因按以下顺序只返回第一个：参数非法、成交编号重复、登记后含 `C0` 的累计额超过 `10^14`。被拒绝的操作不会写入成交、账户、累计额或账期状态。

`Cancel(tid)` 只能撤销当前账期内仍有效的成交。拒绝原因按以下顺序只返回第一个：参数非法、成交不存在、成交已撤销、成交属于已结账期。撤销不会改变当前账期的 `C0`；撤销后，同账户当前账期仍有效的后续成交按原登记次序、从同一个 `C0` 重新计算累计费用。返回值依次为被撤销成交的旧费用和费用确实发生变化的成交列表，列表按登记次序包含 `Tid`、`OldFee`、`NewFee`；费用重算后相同的成交不列入。

`NextPeriod()` 原子推进全部账户的账期。对每个已出现账户（包括本账期没有成交的账户）：

```text
新 C0 = floor((原 C0 + 本账期有效成交金额之和) * ρ / 10000)
```

连续结转逐期复合且每期都向下取整，例如：

```text
C2 = floor(floor(C * ρ/10000) * ρ/10000)
```

账期推进后，旧成交永久不可撤销，但仍占用其 `tid`。所有方法使用同一互斥锁保护，对外等价于某个全序串行执行；`NextPeriod` 对所有账户的结转为一个原子步骤。

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

# 运行 2000 组随机操作序列与朴素重算模型对照（v 输出会打印每组输入、输出与判定依据）
go test -run TestRandomSequencesAgainstNaiveOracle -v ./ontology

# 并发竞态检测
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
