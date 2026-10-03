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

## GSP 广告位竞价结算器

实现位于 `gsp/gsp.go`，入口是 `gsp.NewEngine()`，提供线程安全的 `Register`、`Auction`、`Resolve` 及状态查询。

### 参拍与排序

- `Register(id, bid, q, budget)` 要求 `id != ""`、`1 ≤ bid ≤ 10^6`、`1 ≤ q ≤ 1000`、`1 ≤ budget ≤ 10^12`；登记序号从 1 递增。
- 竞价者持有占用额 `h`（初值 0），可用预算为 `a = budget - h`；`h` 是其全部未结算中标价格之和。
- 连续未点击中标数为 `f`（初值 0），拍卖时冻结有效质量分：

```text
qe = max(1, floor(q × (100 - 10 × min(max(f - 2, 0), 5)) / 100))
```

因此 `f=0,1,2` 无折扣，`f=3` 为 90%，`f≥7` 封顶为 50%。

- `Auction(K, P)` 要求 `1 ≤ K ≤ 100`、`1 ≤ P ≤ 10^6`。
- 同时满足 `bid ≥ P` 与 `a ≥ bid` 才参拍；预算判定使用最高出价 `bid`，不是成交价。
- 对参拍者只进行一次稳定排序：按 `s = bid × qe` 降序，同分时登记序号小者在前，取前 `K` 名。
- 若没有参拍者，返回无参拍者错误且不分配拍卖号。

### GSP 计价与占用

第 `j` 名赢家后还有下一名参拍者时：

```text
p_j = min(bid_j, max(P, floor(s_next / qe_j) + 1))
```

没有下一名参拍者时 `p_j = P`。即使 `s_next` 可被 `qe_j` 整除也仍加 1；第 `K` 名之后的参拍者决定第 `K` 名价格。拍卖号从 1 递增，成交时立即执行 `h += p_j`，同一竞价者在多个未结算拍卖中标会累加占用。

### 结算与质量回馈

`Resolve(拍卖号, 点击集合)` 的点击集合必须是赢家集合的子集且不能有重复 id。每个拍卖只能结算一次：

- 点击赢家：`budget -= p`、`h -= p`、`f = 0`、`q = min(1000, q + floor((1000-q)/8))`。
- 未点击赢家：`h -= p`、`f += 1`、`q = max(1, q - ceil(q/16))`。
- 累计扣费为所有已结算点击价格之和；成交价和 `qe` 在拍卖时冻结，之后结算不会回溯修改。

拒绝原因按优先级只返回第一个：参数非法（含重复点击 id）、竞价者已存在、拍卖不存在、拍卖已结算、点击 id 不是赢家、无参拍者。被拒绝的调用不会改变竞价者、拍卖号、拍卖记录或任何计数。

### 本地验证

若 shell 找不到 Go，可使用 `/usr/local/go/bin/go`；若默认 Go 缓存目录只读，可指定 `GOCACHE=/tmp/go-cache`：

```bash
/usr/local/go/bin/gofmt -w gsp/*.go
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test ./...
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -race -v ./gsp
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
```

`gsp/gsp_test.go` 覆盖边界条件、拒绝优先级、并发和非导出排序比较计数；`gsp/oracle_test.go` 使用独立朴素模拟重放 2000 组随机操作序列，并在失败日志中输出输入、实际输出、朴素模型依据与差异判定。
