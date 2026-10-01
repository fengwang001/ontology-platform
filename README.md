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

## 积分钱包（`wallet` 包）

按到期时刻优先扣减的积分钱包，支持带有效期批次发放、按到期先后消费、按消费单退回，
每笔消费/退回涉及的批次与过期丢弃数量均可精确复现。代码位于 `wallet/wallet.go`。

### 数据模型

- `Grant(id, amount, exp, now)`：发放批次；`amount` 为正 `int64`，批次在
  `[now, exp)` 内有效（**左闭右开**，时刻恰等于 `exp` 即已过期）；批次 `id` 不得复用。
- `Spend(sid, amount, now)`：消费并生成消费单 `sid`，返回各批扣减明细
  `[]DeductItem{BatchID, Amount}`。
- `Refund(sid, amount, now)`：退回消费单 `sid` 的部分已扣数量，返回
  `[]RefundItem{BatchID, Amount, Voided}`；`Voided=true` 表示该批在 `now` 已过期、
  退回数量作废。
- `Balance(now)`：`now` 时仍有效批次的剩余总和；已过期批次的剩余不计入、不可消费。
- `Discarded()`：因「退回时目标批次已过期」而作废的累计数量。
- 所有带 `now` 的操作（含 `Balance`）要求 `now` 不小于此前任何操作的 `now`
  （相等允许）；`Balance(now)` 也会推进时间线。

### 消费次序

只消费 `now < exp` 且仍有剩余的批次：到期时刻 `exp` 小者优先；`exp` 相同时
发放（Grant）先者优先（内部以单调递增的发放序号打破并列）。逐批扣减直到扣够；
有效余额不足时**整体拒绝**，任何批次与消费单均不改动。

### 退回次序与作废规则

- 退回次序与该消费单的扣减次序**相反**：先退最后扣的批次，逐批回退。
- 每批最多退回「该批当初被该消费单扣走且尚未退回」的数量；可多次部分退回。
- 若目标批在退回 `now` 已过期（`now >= exp`，含恰等于 `exp`），退回数量
  **作废**：不增加有效余额，计入 `Discarded()`，但仍算已退回（不可再退）。
  作废数量回到该批剩余桶中，与自然过期未用完的剩余一样既不计余额也不可消费。

### 批次恒等式

任何时刻对每个批次均有：
`剩余 remaining + 累计已扣 deducted − 累计已退回 refunded(含作废) == 发放量`。

### 拒绝顺序（只报第一个原因）

统一顺序：① `now` 小于此前 `now`（`now regression`）→ ② 数量非正
（`non-positive amount`）→ ③ id 重复（Grant：`duplicate batch id`；
Spend：`duplicate spend id`）→ ④ 操作专属校验：

- Grant：`exp <= now` 为 `grant already expired`（发放即过期，单独原因）。
- Refund：消费单不存在 `unknown spend` → 退回超过该单尚未退回总量
  `refund too large`。
- Spend：有效余额不足 `insufficient balance`。

被拒绝的操作不改变任何批次、消费单与时间线。错误以 `*wallet.Error` 返回，
通过 `Reason` 字段（`ReasonNowRegression` 等常量）判定。

### 并发与确定性

所有方法在单个互斥锁下串行化，结果等价于某个串行顺序；配合
`go test -race` 验证无数据竞争。相同操作序列重放得到完全相同的明细与余额。

### 本地验证

```bash
# 全部测试（竞态检测 + 详细日志：输入、输出、判定依据）
go test -race -v ./wallet/

# 对照朴素逐步模拟的 60×400 步随机差分测试
go test -race -run TestDifferentialAgainstNaive -v ./wallet/

# 并发线性化 / 重放确定性 / 规定场景
go test -race -run 'TestConcurrent|TestReplay|TestExpiresAt|TestSameExpiry|TestSpendAcross|TestRefund|TestInsufficient|TestNowRegression' -v ./wallet/

go vet ./...
gofmt -l .
```

`wallet/naive_test.go` 是严格按规则手写的「逐步朴素模拟」（无排序/索引，
每步线性扫描）；`TestDifferentialAgainstNaive` 将同一随机操作流同时喂给两个
实现，逐步比对拒绝原因、扣减/退回明细、余额、过期丢弃与批次恒等式，
失败时打印完整输入/输出日志。
