# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 英式拍卖簿

`auction.NewBook(S, D, R, E, W, X)` 创建带代理出价和软收盘的英式拍卖簿，所有金额与时间均使用 `int64`：

- `S`：起拍价；`D`：加价幅度；`R`：保留价（`0` 表示无保留价）；`E`：初始结束时刻；`W`：延时窗口；`X`：延时量。
- 合法配置要求 `S,D,X >= 1`、`R,W >= 0`、`E >= 1`，且 `S,D,R <= 10^15`。
- 查询只暴露当前价 `Price()`、领先者 `Leader()`、结束时刻 `EndsAt()` 和结算状态；领先者代理上限 `M` 不对外暴露。

### 代理出价

`Bid(bidder, m, now)` 的 `m` 是该竞拍人声明的最高愿付：

- 无人领先时，`m >= S` 接受，`P=S`，领先者为出价人，代理上限 `M=m`。
- 非领先者挑战时，先要求 `m >= P+D`。若 `m > M`，出价人成为领先者且 `M=m`，展示价为 `P=min(m, 旧M+D)`。
- 非领先者 `m <= M` 时，原领先者保位，代理上限不变，展示价为 `P=min(M, m+D)`；`m=M` 也由先出价者保位。
- 领先者只能用严格更大的 `m` 抬高自己的代理上限；接受后仅更新 `M`，`P`、领先者和 `E` 不变。

### 软收盘与结算

- 首个出价或非领先者挑战被接受，并且 `E-now <= W`（相等也触发）时，执行 `E=max(E, now+X)`。
- 领先者自抬上限永不延时；因此延时不会缩短已经存在的结束时刻。
- `Settle(now)` 仅在 `now >= E` 时结算。无人领先或领先者 `M < R` 为流拍；否则赢家为领先者，成交价为 `max(P,R)`。
- 结算结果首次产生后冻结，后续并发或重复调用都返回同一结果。

拒绝原因严格按以下顺序只返回第一个：参数非法、时钟回退、拍卖已结束或已结算、门槛不足、领先者未抬高上限。被拒绝的操作不会改变 `P`、领先者、`M`、`E` 或已接受出价见过的最大 `now`。

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

# 拍卖簿测试（固定种子重放 2000 组随机序列，并输出逐步输入、输出和判定依据）
GOCACHE=/tmp/go-build-ontology go test -run TestRandomSequencesAgainstNaiveSimulation -v ./auction

# 若本机默认 Go 构建缓存只读，可统一指定 /tmp 缓存
GOCACHE=/tmp/go-build-ontology go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
