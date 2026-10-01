# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 订阅套餐月中变更账单引擎（billing 包）

`billing` 包实现按日折算的账单引擎：对一个计费周期内的多次升降级逐次生成折退与补收账单行。

### 计费规则

- 周期天数 `D`（1 至 366），套餐表为名称到整周期标价（分，0 至 1e12）。
- 周期第 0 天按初始套餐预收全价，记为第一行（`PREPAY`）。
- 第 `day` 天变更套餐（`day` 为已过去的整天数，新套餐当日起生效，剩余天数 `r = D - day`），依次生成两行：
  - `REFUND` 折退行（金额为负）：`当前套餐标价 * r / D`，**向下取整**（折退不多退）；按当前生效套餐的标价计，而非已支付金额，因此一个周期内多次变更逐次独立。
  - `SURCHARGE` 补收行（金额为正）：`新套餐标价 * r / D`，**向上取整**（补收不少收）。
- 行的生成顺序：每个周期先一行 `PREPAY`，之后每次变更依次追加一行 `REFUND`、一行 `SURCHARGE`。
- 结算（`Settle`）返回本周期全部行，应收总额等于各行代数和（折退为负）；随后开启新周期，按结算时的当前套餐在第 0 天重新预收全价。
- 不整除时同一标价的折退与补收恰好相差一分（`floor` 与 `ceil` 之差），舍入方向固定，相同操作序列重放得到逐行相同的账单。

### 变更拒绝顺序（只报第一个）

1. 未知套餐（`ErrUnknownPlan`）
2. 与当前套餐相同（`ErrSamePlan`）
3. `day` 不在 1 至 D−1（`ErrInvalidDay`）
4. `day` 等于上一次变更日（`ErrSameDayChange`，本周期尚无变更时不检查）
5. `day` 小于上一次变更日（`ErrOutOfOrderDay`，同上）

构造时 `D` 越界、套餐价格越界、初始套餐未知同样拒绝（`ErrInvalidDays` / `ErrInvalidPrice` / `ErrUnknownPlan`）。被拒绝的操作不改变套餐、变更日与已生成的行。

### 并发

变更、结算与查询均可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；任何时刻已生成各行之和等于按定义重算的值。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志，日志打印输入、输出与判定依据）
go test -race -v ./billing

# 单个用例，例如取整方向
go test -run TestFloorCeilOneCent -v ./billing

# 与朴素公式重算实现的对照 / 重放一致性
go test -run TestReplayMatchesNaive -v ./billing
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
