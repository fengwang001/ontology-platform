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

## 广告预算节流器

`budgetthrottle` 包按权重曲线匀速投放日预算，并支持受限追赶与退款回补。

### 累计目标曲线

设日预算为 `B`，时段数为 `n`，时段长度为 `L`，权重为 `w_0 ... w_{n-1}`，累计权重
`W_i = w_0 + ... + w_i`。第 `i` 个时段结束位置的累计花费目标为：

```text
tgt_i = floor(B * W_i / W_{n-1})
tgt_-1 = 0
q_i = tgt_i - tgt_{i-1}
```

`B * W_i` 可能超过 64 位范围，构造时使用 `math/big.Int` 计算并将结果存入目标数组。
由于每一步都向下取整，`q_i` 可能为 `0`。运行时只直接读取当前时段和上一时段的目标值，
不遍历所有时段。

时刻 `now` 的时段号为 `floor(now / L)`，合法范围是 `0 <= now < n*L`。

### 缺口与追赶额度

进入时段 `i` 后，第一个被接受的 `Try`、`TryUpTo` 或 `Refund` 会先记录当时的总花费
`s_i`，并立即固定该时段额度：

```text
d_i = max(0, tgt_{i-1} - s_i)
A_i = min(q_i + min(d_i, floor(q_i*m/100)), B-s_i)
```

跳过多个无操作时段时，`d_i` 使用当前时段之前的累计目标，因此缺口包含所有被跳过时段；
但追赶量仍只受当前时段自己的 `floor(q_i*m/100)` 限制。`A_i` 一旦固定，不会因同一时段内
后续退款或花费变化而重算。

当前时段的已花费记为 `ps`。`Try(a, now)` 只有在 `ps+a <= A_i` 时全额放行；
`TryUpTo(a, now)` 放行 `min(a, A_i-ps)`，放行值至少为 `1` 才算成功。

### 退款影响

`Refund(a, now)` 同样会先按退款前的 `spent` 固定当前时段 `A_i`，随后执行：

```text
spent -= a
ps -= min(ps, a)
```

退当前时段已产生的花费时，会降低 `ps`，在固定的 `A_i` 内重新腾出本时段额度；退更早
时段的花费时，`ps` 可能为 `0` 或不足，因此只降低 `spent`。这会使后续时段的
`tgt_{i-1}-spent` 缺口变大，追赶额度在下一时段体现。退款额不能大于当前 `spent`。

拒绝原因按以下优先级只返回第一个：参数非法、时钟回退、日预算不足、单笔过大、
被限速、退款过多。被拒绝的操作不会改变 `spent`、`ps`、当前时段、固定额度或最大已接受
时刻。所有公开方法使用互斥锁保护，并发结果等价于某种串行顺序。

### 本地验证

```bash
go test ./...
go test -race ./budgetthrottle
go test -run TestRandomSequencesMatchNaiveSimulation -v ./budgetthrottle
```

随机测试使用固定种子重放 2000 组操作序列，并将逐步输入、输出、拒绝原因和状态写入
`budgetthrottle/random-sequences.log`，同时与按规则直接实现的朴素模拟器逐项对照。
