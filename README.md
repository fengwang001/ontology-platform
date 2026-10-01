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

## 限价订单簿撮合引擎（`orderbook` 包）

价格优先、时间优先的限价订单簿，构造时给定最小报价单位：

```go
eng, err := orderbook.NewEngine(tick int64) // tick 必须为正，否则返回 ErrTickNonPositive
trades, err := eng.Submit(orderbook.Order{ID: 1, Side: orderbook.Buy, Price: 100, Qty: 5})
removed, err := eng.Cancel(id int64)
err = eng.Amend(id, newQty int64)
levels, err := eng.Depth(orderbook.Sell, 10) // []Level{{Price, Qty, Count}}
```

### 撮合条件与成交价

- 买单与簿上卖单在 `卖单价 <= 买单价` 时成交；卖单与买单对称（`买单价 >= 卖单价`）。价格相等即成交。
- 先取对手方最优价位（卖方最低价 / 买方最高价），同价位按到达序号（FIFO）依次吃单。
- 每笔成交价恒为被动方（簿上订单）价格，与主动单价格无关；成交量为双方剩余量的较小值。
- 主动单撮合后若仍有剩余，以原价格挂入该价位队尾；不存在只吃不挂或立即成交的特殊单型。任何时刻最优买价严格小于最优卖价。
- 成交按发生次序返回：`Trade{PassiveID, AggressorID, Price, Qty}`。

### 改量（Amend）对排队位置的影响

`newQty` 为该在簿订单的新剩余量（正整数且 `<= 1e12`）：

- `newQty < 当前剩余`：保持原排队位置。
- `newQty == 当前剩余`：成功但什么都不做（不改簿、不取新序号）。
- `newQty > 当前剩余`：移到同价位队尾，视为重新到达，获得新的到达序号。

### 拒绝顺序（只报第一个原因）

- `Submit`：`id 已被使用`（无论在簿、已成交、已撤销）→ `方向非法` → `价格非正/超限/非 tick 整数倍` → `数量非正/超限`。
- `Cancel` / `Amend`：`id 从未出现`（`ErrUnknownID`）→ `id 已不在簿`，其中已全部成交（`ErrAlreadyFilled`）与已撤销（`ErrAlreadyCancelled`）是两种不同错误 → `Amend` 再检查 `newQty`（`ErrInvalidQty`）。
- 被拒绝的操作不改变簿、成交序列与到达序号。错误均为哨兵错误，用 `errors.Is` 判别。

### 并发、不变量与可复现

- 所有方法在互斥锁下串行化，并发调用结果等价于某个串行顺序。
- 对每个订单恒有：`提交数量 + Σ(改量后剩余 - 改量前剩余) == 已成交 + 已撤销 + 在簿剩余`。
- 到达序号只在「挂单入簿」与「改大后移到队尾」时分配，相同操作序列重放得到完全相同的成交序列。

### 本地验证

```bash
go test -race -v ./orderbook       # 场景测试 + 与逐步朴素模拟器的随机对照
go test -v -run TestAgainstNaiveRandom ./orderbook  # 日志打印每步输入/输出与判定依据
go vet ./...
```

随机对照测试把同一批操作（含刻意构造的非法提交、撤单、改量、深度查询）同时施加于生产引擎与一份按规格逐行写成的朴素模拟器（`engine_naive_test.go`），逐步比对成交、错误、深度及数量不变量。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
