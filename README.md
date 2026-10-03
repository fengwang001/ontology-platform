# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 现货容量市场（spotmarket 包）

`spotmarket` 实现带统一出清价、中断免费小时与整点边界规则的现货容量市场。

### 排名与出清价

- 所有未终止的请求（运行中与等待中）按 **(bid 降序, 提交序号升序)** 排名，提交序号按成功的 `Request` 递增。
- 排名前 K（K 为当前容量，可由 `SetCapacity` 修改）的请求构成运行集合，其余等待。
- 出清价为第 K+1 名的 bid；不足 K+1 个请求时为底价 Pmin。
- 价格历史初值为 `[(0, Pmin)]`；每个被接受的操作在时刻 t 先应用自身再重算排名，出清价变化时记录 `(t, 价)`，同一 t 的多次记录以最后一次为准。`price(x)` 取历史中时刻不大于 x 的最后一条的价。

### 运行段的开始与结束

- 原等待但进入前 K 的请求开始新的运行段；原在运行但跌出前 K 的请求，其运行段以「市场中断」结束（请求仍留在集合中，之后若再进入前 K 则开始全新的一段并重新计小时）。
- `Terminate` 移除请求：若正在运行，其运行段以「用户终止」结束；等待中被终止不产生任何费用。
- `Request` / `Terminate` / `SetCapacity` 返回本次事件清单：先结束事件再开始事件，各自按 id 升序；结束事件含该段费用。`Bill(id)` 返回该请求全部已结束运行段的费用之和。

### 计费口径

- 运行段 `[s, e)` 结束时结算：用户终止收 `ceil((e−s)/3600)` 个小时，市场中断收 `floor((e−s)/3600)` 个小时（不足一小时免费，恰在整点边界不多收）。
- 第 k 个小时（k 从 0 起）的价为 `price(s + 3600×k)`，即该小时起点时刻的价。
- 费用使用 `big.Int` 精确计算，不会溢出；相同操作序列重放得到完全相同的事件与费用。

### 拒绝规则

被拒绝的操作不改变请求集合、排名、价格历史与最大已接受时刻 t。各操作按顺序只报第一个原因（哨兵错误，可用 `errors.Is` 区分）：

- `Request`：`ErrInvalidParam` → `ErrClockRegression` → `ErrDuplicateID`（含已终止的 id）。
- `Terminate`：`ErrInvalidParam` → `ErrClockRegression` → `ErrNotFound` → `ErrAlreadyTerminated`。
- `SetCapacity`：`ErrInvalidParam` → `ErrClockRegression`。
- 构造参数越界（K 不在 1..1000 或 Pmin 不在 1..10^9）整体拒绝，返回 `ErrInvalidConfig`。

### 并发

所有操作与查询（`Request` / `Terminate` / `SetCapacity` / `Bill` / `PriceAt` / `RunningIDs`）均可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。

### 本地验证

```bash
# 单元测试（含题目示例、整点边界、并列排名、SetCapacity 等）
go test ./spotmarket/

# 2000 组随机序列与朴素模拟对照（-v 打印输入、输出与判定依据）
go test -v -run TestRandomSequences ./spotmarket/

# 竞态检测
go test -race ./...
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
