# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 广告预算节流器（`throttler` 包）

按权重曲线在一天内匀速投放日预算，支持缺口追赶（有上限）与退款回补。
所有操作并发安全，结果等价于某个串行顺序；相同操作序列重放得到完全
相同的放行结果与额度。

### 累计目标曲线与取整

- 一天划分为 `n` 个长度为 `L` 的时段，时段 `i` 的权重为 `w_i`，
  前缀和 `W_i = w_0 + ... + w_i`。
- 时段 `i` 结束时的累计投放目标为
  `tgt_i = floor(B * W_i / W_{n-1})`，`tgt_{-1} = 0`。
  乘积 `B * W_i` 可达 10^21，内部用 128 位运算（`math/bits`）精确计算，
  不遍历时段（构造时预存前缀和，单次取值 O(1)）。
- 时段预算 `q_i = tgt_i - tgt_{i-1}`，取整可能使某些时段 `q_i = 0`。

### 缺口与追赶额度

进入时段 `i` 后的第一个被接受操作（`Try` / `TryUpTo` / `Refund`）之前，
若已花费总额为 `s_i`，则：

- 缺口 `d_i = max(0, tgt_{i-1} - s_i)`（跳过若干无操作时段时按全部被
  跳过时段累计）；
- 追赶上限 `cap_i = floor(q_i * m / 100)`，只受当前时段 `q_i` 限制；
- 时段额度 `A_i = min(q_i + min(d_i, cap_i), B - s_i)`，在该时段内固定
  不变，时段内已花费 `ps` 从 0 开始。

`Try(a, now)` 要求 `ps + a <= A_i`；`TryUpTo(a, now)` 放行
`x = min(a, A_i - ps)`（`x >= 1` 才成功并返回 `x`）；
`Allowance(now)` 只读返回 `A_i - ps`。

### Refund 对当前时段的影响

`Refund(a, now)` 先按上述规则用退款**之前**的 `spent` 固定所在时段的
`A_i`，再令 `spent -= a`、`ps -= min(ps, a)`。因此：

- 退的是当前时段的花费（`ps` 足够）时，等额腾出本时段额度；
- 退的是更早时段的花费（`ps` 为 0 或不足）时，只降低 `spent`，不腾出
  本时段额度，但下一时段的缺口 `d` 随之变大，追赶空间增加。

### 拒绝原因（按优先级只报第一个）

参数非法 → 时钟回退 → 日预算不足 → 单笔过大 → 被限速 → 退款过多。
被拒绝的操作不改变 `spent`、`ps`、当前时段及其 `A_i` 与最大 `now`。

### 本地验证

```bash
# 全部单元测试（含 2000 组随机序列与朴素模拟对照、并发线性化测试）
go test ./throttler/

# 竞态检测
go test -race ./throttler/

# 查看随机对照日志（输入、输出与判定依据）
go test -run TestRandomAgainstNaive -v ./throttler/
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
