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

## 限价订单簿撮合引擎（`orderbook` 包）

价格优先、时间优先的限价订单簿，支持并发调用，结果等价于某个串行顺序。

### 基本规则

- 构造：`NewOrderBook(tick)`，`tick` 为正 int64，否则返回 `ErrInvalidTick`。
- 订单：`(id, side, price, qty)`，`side ∈ {Buy, Sell}`；`price`、`qty` 为正 int64 且 ≤ 1e12，`price` 必须是 `tick` 的整数倍。
- 订单 id 一经成功提交即被登记（无论此后已成交、已撤销还是在簿），不得再次使用；被拒绝的提交不占用 id。

### 撮合条件与成交价

- 买单与簿上卖单撮合的条件：卖单价格 ≤ 买单价格；卖单与买单对称（买单价格 ≥ 卖单价格）。
- 先取对手方最优价位（卖方最低价、买方最高价），同价位按到达序号小者先。
- 每笔成交价为**被动方**（簿上已有订单）的价格，数量为双方剩余量的较小者。
- 成交按发生次序返回 `(被动单 id, 主动单 id, 价格, 数量)`。
- 主动单撮合完后若仍有剩余，以原价挂入簿，进入该价位队尾；任意时刻最优买价严格小于最优卖价。

### 撤单与改量

- `Cancel(id)` 返回被撤销的剩余数量。
- `Amend(id, newQty)` 修改在簿订单的剩余数量：
  - `newQty` 小于当前剩余：保持原排队位置；
  - `newQty` 大于当前剩余：移到同价位队尾，视为重新到达并获得新的到达序号；
  - `newQty` 等于当前剩余：成功的空操作，位置不变。
- `Depth(side, n)` 返回该方向最优的 n 个价位 `(价格, 总剩余量, 订单数)`：买方价高在前、卖方价低在前，只含剩余量大于 0 的价位。

### 拒绝顺序（只报第一个）

1. 订单 id 已被使用（`ErrDuplicateID`）
2. 方向非法（`ErrInvalidSide`）
3. 价格非正、超限或不是 tick 倍数（`ErrInvalidPrice`）
4. 数量非正或超限（`ErrInvalidQty`）

`Cancel`/`Amend` 的 id 错误有三种可区分原因：从未出现（`ErrUnknownID`）、已全部成交（`ErrOrderFilled`）、已撤销（`ErrOrderCancelled`）；`Amend` 的 `newQty` 非正或超限为 `ErrInvalidNewQty`。被拒绝的操作不改变簿、成交与到达序号。

### 不变量

- 对每笔曾提交的订单：提交数量 + 历次改量的（新剩余 − 旧剩余）之和 ≡ 已成交量 + 已撤销量 + 在簿剩余量。
- 相同的操作序列重放得到完全相同的成交序列。

### 本地验证

```bash
# 全部测试（含与朴素逐步模拟的差分对照、确定性重放、并发用例）
go test ./orderbook/

# 竞态检测 + 查看日志中的输入、输出与判定依据
go test -race -v ./orderbook/
```
