# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 定额月供贷款引擎

贷款引擎位于 `loan` 包，金额全部以“分”为单位，利率为百万分比整数，所有计算使用 `int64`。

- `New(P, r, A)` 创建本金 `P`、初始月利率 `r`、月供 `A` 的贷款；第 `n` 期利息为 `floor((bal × r_n + 500000) / 1000000)`，即正商四舍五入、恰半入。
- 利率选择：`SetRate(r2, k0)` 登记从绝对期序号 `k0` 起生效的利率；同一 `k0` 覆盖，多个 `k0` 并存，第 `n` 期取所有 `k0 <= n` 中最大的 `k0`，无匹配时使用初始利率。
- `Pay()`：宽限期内只还利息，本金和余额不变，消耗一期宽限；非宽限期若 `bal + interest <= A`，该期为末期并支付 `bal + interest`；否则支付 `A`、本金为 `A - interest`。
- `Holiday(c)` 登记接下来的 `c` 个绝对期序号为宽限期。利率仍按绝对期序号在宽限期内变更；宽限期不检查 `A` 是否足以覆盖利息。
- `Prepay(x)` 只缩短期限、不改变月供、宽限期和利率表。允许 `x >= A` 或 `x == bal`；`x == bal` 立即结清。违约金为 `floor(x × φ / 10000)`，其中 `k < 12` 时 `φ=300`，`12 <= k < 36` 时 `φ=100`，`k >= 36` 时 `φ=0`。
- 登记校验：`New`、`SetRate`、`Holiday` 会从候选状态开始模拟最多 601 期。非宽限、非末期若出现 `A <= interest`，先报“不可摊还”；否则必须在第 600 期或之前结清，第 601 期才结清或 601 期后仍未结清时报“期限超限”。被拒绝的登记不会修改任何状态。
- 错误优先级：`New` 为参数非法、不可摊还、期限超限；`Prepay` 为参数非法、已结清、超额、低于最小额；`SetRate` 为参数非法、已结清、生效期已过、不可摊还、期限超限；`Holiday` 为参数非法、已结清、已有宽限、不可摊还、期限超限。
- `Remaining()` 不改变状态，按当前利率表与宽限期逐期模拟，返回包含宽限期在内的剩余期数；已结清为 0。
- 所有方法由互斥锁保护，并发调用的结果等价于某个串行执行顺序。

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

# 打印贷款引擎示例、边界与 2000 组随机朴素对照日志
go test -v ./loan

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
