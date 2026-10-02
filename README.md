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

## 服务等级违约积分结算

实现位于 `sla/sla.go`，入口为 `sla.New(config)`，配置使用 `sla.Config`：

- `LengthMinutes`：月长 `L`
- `JitterMinutes`：抖动下限 `g`
- `Tier1Availability`、`Tier2Availability`、`Tier3Availability`：百万分比档位，满足 `T1>T2>T3`
- `Tier1Credit`、`Tier2Credit`、`Tier3Credit`：对应积分百分点，满足 `c1<c2<c3`
- `EscalationStep`、`EscalationCapMonths`：连续违约升级步长与封顶月数
- `AnnualCreditLimit`：年度积分额度 `Y`，单位分
- `MonthlyExcludeLimit`：每月排除窗口并集长度上限 `ex`

### 区间结算顺序

`Close()` 严格按以下顺序处理，不提前丢弃短故障：

1. 将本月所有 `Report(a,b)` 的半开区间 `[a,b)` 排序并归并；重叠区间以及首尾相接区间合并。
2. 从归并后的故障区间中扣除全部排除窗口的并集；一个故障区间可能被切成左右两个残段。
3. 对扣除后的每个连续残段判断长度，严格小于 `g` 才丢弃，长度等于 `g` 保留。
4. 保留残段总长为 `D`，保证 `0≤D≤L`。

例如 8 分钟故障扣除后只剩 3 分钟，当 `g=4` 时该 3 分钟残段会在扣除后被丢弃；这与“先丢整段再扣排除”的结果不同。

`Revoke(a,b)` 只删除一份完全相同的已登记故障区间；`AddExclude(a,b)` 按排除窗口的并集计长，重叠或相同窗口不会重复占用 `ex`。

### 可用度与档位

可用度按整数向下取整：

```text
A = floor((L-D) × 1_000_000 / L)
```

基础积分百分点：

- `A≥T1`：`base=0`
- `T2≤A<T1`：`base=c1`
- `T3≤A<T2`：`base=c2`
- `A<T3`：`base=c3`

档位边界使用“大于等于”，因此 `A` 恰等于 `T1`、`T2` 或 `T3` 时归入较高一档。

### 连续违约与年度封顶

- `base=0` 时本月积分为 0，连续违约月数 `s` 归零。
- `base>0` 时，先计算 `pct=min(100, base+st×min(s,sm))`。
- 再按 `credit=ceil(fee×pct/100)` 向上取整。
- 年度截断最后执行：`credit=min(credit, Y-本年已发放)`。
- 发放后更新该年已发放积分，并令 `s=s+1`。
- 月份从 0 开始，年号为 `floor(month/12)`；跨年时年度额度重置，但 `s` 不重置。

### 错误与并发

非法构造参数、费用越界、空区间或区间越界统一返回 `sla.ErrInvalidArgument`；状态错误使用 `sla.ErrMonthAlreadyOpen` 或 `sla.ErrNoOpenMonth`；排除上限使用 `sla.ErrExcludeLimitExceeded`；撤销不存在使用 `sla.ErrNotFound`。

错误优先级为参数非法、状态错误、排除超限、不存在。被拒绝的操作不会写入任何状态。所有方法由互斥锁保护，并发调用等价于某个合法串行顺序。

### 本地验证

结算器测试位于 `sla/sla_test.go` 与 `sla/oracle_test.go`：

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机序列的输入、输出与判定依据
go test ./sla -run TestRandomSequencesAgainstMinuteOracle -v -count=1
```

随机对照测试使用固定种子，并以逐分钟故障/排除数组作为朴素预言机，独立复算区间并集、扣除残段、抖动过滤、可用度、档位、升级、年度额度、连续违约月数和年度已发放积分。
