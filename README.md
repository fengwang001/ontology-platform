# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 订阅套餐月中变更折账引擎（`billing` 包）

`billing.Engine` 实现一个计费周期 D 天（`1 <= D <= 366`）内、多次升降级逐次按日折算的账单引擎。金额单位为分，全部用整数运算，无浮点参与。

### 行的生成顺序

1. 开周期（`New` 与每次 `Settle` 之后）：生成第 1 行，第 0 天按当前套餐整周期标价预收（`LineCharge`，金额 = 标价）。
2. 每次在第 `day` 天（`1 <= day <= D-1`）变更到新套餐，剩余天数 `r = D - day`，严格按以下顺序追加两行：
   - 折退行（`LineRefund`）：金额为负，按**当前生效套餐**（旧套餐）的标价折算
     `-floor(旧套餐标价 * r / D)`；
   - 补收行（`LineCharge`）：金额为正，按**新套餐**的标价折算
     `ceil(新套餐标价 * r / D)`。
3. `Settle`：返回本周期全部行的副本与各行代数和（折退为负），随后立即开启新周期，并在新周期第 0 天按结算时的当前套餐重新预收全价。

因此同一周期内多次变更逐次独立：第二次折退按第二个套餐（即当时生效套餐）的标价计算，与第一次已支付/已折退金额无关。

### 取整方向

- 折退**向下取整**（不多退）：`floor(p*r/D) = p*r/D`（Go 整数除法，操作数非负）。
- 补收**向上取整**（不少收）：`ceil(p*r/D) = (p*r + D - 1)/D`。
- 不能整除时两者可相差 1 分，例如 `D=3, p=100, r=2`：折退 `-66`、补收 `+67`；能整除时两者相等。

### 拒绝规则（按此顺序只报第一个）

`Change` 的校验顺序为：未知套餐 → 与当前套餐相同 → `day` 不在 `[1, D-1]` → `day` 等于上一次变更日（`ReasonDayRepeated`）→ `day` 小于上一次变更日（`ReasonDayOutOfOrder`）。后两项仅在周期内已有变更时检查（`LastDay == -1` 时跳过）。构造 `New` 时依次拒绝：`D` 越界、任一套餐价格越界（允许 `[0, 1e12]`）、初始套餐未知。被拒绝的操作不改变当前套餐、变更日与已生成的任何行；错误类型为 `*billing.Error`，可通过 `Reason` 字段区分原因。

### 并发与确定性

- `Change` / `Settle` / `Query` 可并发调用，内部以单一互斥锁线性化，结果等价于某个串行顺序；`Query` 与 `Settle` 返回行的副本，调用方持有切片后不受后续操作影响。
- 任意快照中各行 `Amount` 代数和恒等于 `Snapshot.Total`，首行恒为第 0 天预收行，折退行日期严格递增。
- 相同的操作序列重放得到逐字段相同的行（含 `Period`、`Seq`、`Kind`、`Day`、`Plan`、`Price`、`Remain`、`Amount`）。

### 本地验证

```bash
# 全量测试（环境若没有 go，可使用 /usr/local/go/bin/go）
go test ./...

# 竞态检测 + 多遍重放 + 详细日志（日志含每个用例的输入、逐行输出与判定依据）
go test -race -count=3 -v ./billing

# 只看某个场景
go test -v -run 'TestRoundingFloorVsCeilOneCentGap' ./billing

# 格式化与静态检查
gofmt -l .
go vet ./...
```

若 Go 构建缓存目录只读，可指定缓存目录：`export GOCACHE=/tmp/go-cache`。

测试中的朴素对照实现（`billing_test.go` 内的 `naiveEngine`）不与引擎共享任何算术或存储代码，直接按上述定义逐行重算，每个场景的每一步及结算结果都与引擎逐行比对。

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
