# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 英式拍卖簿（`auction` 包）

带代理出价（proxy bidding）与软收盘延时（soft close）的英式拍卖簿，
并发安全，行为可精确复现。金额与时刻均为 `int64`。

### 构造参数

`Config{StartPrice: S, MinIncrement: D, ReservePrice: R, EndTime: E, ExtendWindow: W, ExtendBy: X}`。
`S、D < 1`，`R < 0`，`E < 1`，`W < 0`，`X < 1`，或 `S、D、R > 10^15` 时整体以
`ErrInvalidConfig` 拒绝。`R = 0` 表示无保留价。

### 代理出价抬价公式

`Bid(bidder, m, now)` 中 `m` 是该竞拍人的最高愿付（代理上限 `M`，不对外暴露）：

- 无领先者：要求 `m >= S`，成交展示价 `P = S`，出价人成为领先者，`M = m`。
- 出价人不是领先者：要求 `m >= P + D`。
  - `m > M`：出价人成为新领先者，`M = m`，`P = min(m, 旧M + D)`。
  - `m <= M`（含相等，先出价者保位）：领先者与 `M` 不变，`P = min(M, m + D)`。
- 出价人就是领先者：要求 `m > M`（严格抬高），仅更新 `M`，`P` 与领先者不变。

拒绝原因按固定顺序只报第一个：`ErrInvalidBidParam`（竞拍人为空或 `m` 越界）→
`ErrClockBackward`（`now` 回退）→ `ErrAuctionEnded`（`now >= E` 或已结算）→
`ErrBelowThreshold`（门槛不足）→ `ErrNotRaisingMax`（领先者未抬高上限）。
被拒绝的出价不改变任何状态（含已见最大 `now`）。

### 软收盘延时

仅当非领先者（含首个出价）的出价被接受、且 `E - now <= W`（恰等也延时）时，
`E = max(E, now + X)`；领先者自抬上限从不延时；`now + X < E` 时 `E` 不缩短。

### 结算

`Settle(now)` 要求 `now >= E`，否则报 `ErrNotEnded`。无领先者或 `M < R` 为流拍；
否则成交，赢家为领先者，成交价为 `max(P, R)`。结算结果一经产生不再变化，
并发或重复调用均得到同一结果。

### 本地验证

```bash
go test ./auction/          # 单元测试 + 2000 组随机序列对照朴素模拟
go test -race ./auction/    # 竞态检测
go test -v -run TestRandomReplayAgainstModel ./auction/  # 查看逐步输入/输出/判定依据日志
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
