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

`ontology` 包（`gsp.go`、`gsp_query.go`）实现带质量分加权、底价、预算占用与质量回馈的
广义第二价格（GSP）拍卖。线程安全，所有操作可并发调用，结果等价于某个串行顺序。

### 数据模型

- `Register(id, bid, q, budget)`：`id` 非空；`bid∈[1,10^6]`（每次点击出价）；
  质量分 `q∈[1,1000]`；`budget∈[1,10^12]`。登记序号从 1 递增。
- 每个竞价者维护：占用额 `h`（初值 0）、可用预算 `a = budget − h`、
  连续未点击中标数 `f`（初值 0）。
- 有效质量分（排序与计价一律使用 `qe`，不使用 `q`）：

```
qe = max(1, floor(q × (100 − 10×min(max(f−2,0), 5)) / 100))
```

  `f=0,1,2` 无折扣；`f=3` 为 90%；之后每多一次未点击再降 10 个百分点；
  `f≥7` 封顶 50%。

### 参拍条件与排序

`Auction(K, P)`：`K∈[1,100]` 个广告位，底价 `P∈[1,10^6]`。参拍者必须同时满足：

- `bid ≥ P`；
- `a ≥ bid`（按自己的最高出价判定，而非最终计价）。

参拍者按分值降序排序，同分按登记序号小者在前：

```
s = bid × qe
```

实现上对参拍者集合只排序一次（`sort.Sort`），不为每个广告位重复扫描全部竞价者；
非导出计数器 `sortCompares` 记录该次排序的比较次数，`TestSingleSortCounter`
在竞价者 100 与 10000 两档验证比较次数为单次排序量级（上界 `2n·ceil(log2(n+1))`），
而逐广告位全量扫描的 `K·n` 量级会超过该上界。

### 计价公式

取排序后前 K 名为赢家。第 j 名赢家的计价：

```
存在下一名参拍者（含第 K 名之后者）:
  p_j = min(bid_j, max(P, floor(s_next / qe_j) + 1))
无下一名:
  p_j = P
```

- 整除时仍 `+1`（GSP 的一单位加价）；
- `floor(s_next/qe_j)+1` 低于底价时由 `P` 托底，高于自己出价时由 `bid_j` 封顶；
- 因此恒有 `P ≤ p_j ≤ bid_j` 且 `p_j × qe_j ≥ s_next`；
- `qe` 与价格在 Auction 时刻冻结并随中标记录保存，之后 Resolve 不改已出价格。

拍卖成功即产生递增拍卖号（从 1 开始）；每个赢家 `h += p_j`（预算占用）。
同一竞价者可在多个未结算拍卖中重复中标，占用逐笔累加。

### 结算规则

`Resolve(拍卖号, 点击集合)`：点击集合必须是该拍卖赢家 id 的子集且不含重复 id。

- 点击赢家：`budget -= p_j` 且 `h -= p_j`，`f = 0`；
- 未点击赢家：仅 `h -= p_j`，`f += 1`（不扣预算）。

任何时刻对每个竞价者都有 `0 ≤ h ≤ budget`，且 `h` 恰等于其全部未结算中标
（可能跨多场拍卖）的 `p_j` 之和；累计扣费恰等于全部已结算点击赢家的 `p_j` 之和。

### 质量分回馈

结算后立即更新（结果即时影响之后的拍卖）：

```
点击:   q = min(1000, q + floor((1000 − q) / 8))
未点击: q = max(1,  q − ceil(q / 16))
```

### 拒绝原因（按此顺序只报第一个）

1. 参数非法：id 为空、数值越界、点击集合含重复 id；
2. 竞价者已存在（Register）；
3. 拍卖不存在（Resolve）；
4. 拍卖已结算；
5. 点击集合含非赢家；
6. 无参拍者（Auction 无任何满足条件者，不产生拍卖号）。

被拒绝的操作不改变任何竞价者状态、拍卖号与拍卖记录。错误通过 `*OpError`
返回，其 `Kind` 字段（`ErrInvalidArgument` 等）可程序化区分。

### 查询接口

- `GetBidder(id)`：`bid/q/budget/h/f/seq` 快照与 `Available()`（即 `a`）；
- `GetAuction(id)`：赢家清单（id、冻结的 `p` 与 `qe`、是否点击）与结算标记；
- `NumBidders()`、`NumAuctions()`、`SortCompares()`。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（含输入、输出、判定依据与 2000 组随机差分结果）
go test -race -v ./...

# 只看规则用例 / 排序计数 / 2000 组随机对照 / 重放日志
go test -v -run 'TestSpecExample|TestBidFloor|TestBudget|TestTie|TestPrice|TestDivisible|TestRunnerUp|TestHold|TestF|TestQe|TestQuality|TestResolve|TestRegister'
go test -v -run TestSingleSort
go test -v -run TestRandomDifferential
go test -v -run TestReplayLog
```

测试覆盖：bid/a 的恰等与差 1 边界、同分按序号、bid 封顶与底价托底、整除 +1、
末位取 P、第 K 名之后的参拍者计价、重复中标占用累加导致退出、f 各折扣档
（2 无折扣 / 3 九折 / ≥7 五折）、`q=1` 时 `qe=1`、点击清零 f、qe 冻结、
质量分取整方向（含 q=1000 与 q=1）、空/全点击、重复结算与非赢家拒绝、
被拒不改状态、并发线性一致（`-race`）、确定性重放，以及与独立朴素模拟
（`gsp_naive_test.go`，不共享生产代码）对照 2000 组随机操作序列。
