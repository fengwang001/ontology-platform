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

## 分周期配额结转账本（`quota` 包）

`quota.Ledger` 把跨周期的用量记录按时间比例摊入各周期，并把未用额度按
上限结转。时间坐标为 `int64`，单位由调用方决定。

创建参数：起点 `t0`、周期长度 `P`、每周期基础额度 `Q`、结转上限 `M`
（整数）。第 `k` 个周期为半开区间 `[t0+kP, t0+(k+1)P)`，`k` 从 0 起。

### 摊入公式

记录 `(id, [s,e), amount)` 与周期 `k` 的重叠长度记为
`overlap_k = |[s,e) ∩ [t0+kP, t0+(k+1)P)|`，区间总长 `L = e-s`。

- 每个被触及周期先分得 `floor(amount × overlap_k / L)`（向下取整）；
- 各周期取整后产生的余数 `amount - Σ floor(...)` 全部加到该记录所触及的
  **最后一个周期**，因此 `Σ_k 摊入量 ≡ amount`，严格守恒；
- 区间为半开：右端点 `e` 恰好落在周期边界时**不触及**下一周期
  （最后触及周期按 `e-1` 定位）；
- `amount = 0` 的记录合法，摊入量恒为 0。

### 结转与超额

记周期 `k` 的摊入用量为 `u_k`，转入量为 `c_k`，有效额度为 `q_k`：

- `c_0 = 0`；
- `c_k = min(M, max(0, q_{k-1} - u_{k-1}))`（`k ≥ 1`）；
- `q_k = Q + c_k`；
- 超额量 `o_k = max(0, u_k - q_k)`，只如实报告，不拒绝记录，也不产生
  负结转（超额后下一周期转入量为 0）。

### 顺序无关与并发

不保存任何周期性中间结果；每次 `Query` 都按当前全部记录（以 ID 排序
保证确定性）现算。因此：

- 晚到记录会改变此前周期的用量与结转，并顺延影响之后所有周期；
- 同一记录集无论到达顺序如何，查询结果完全相同，可重放复现；
- `Add` / `Query` 内部使用读写锁，可并发调用。

### 拒绝原因（可区分的哨兵错误）

- 创建：`ErrInvalidBaseQuota`（`Q ≤ 0`）、`ErrInvalidPeriodLength`
  （`P ≤ 0`）、`ErrNegativeCarryCap`（`M < 0`）；
- 记录：按 `s < t0` → `e ≤ s` → `amount < 0` → 标识重复的顺序，只报
  第一个原因（`ErrStartBeforeT0`、`ErrInvalidInterval`、
  `ErrNegativeAmount`、`ErrDuplicateID`）；
- 查询：`k < 0` 返回 `ErrNegativePeriod`；
- 被拒绝的操作不写入任何状态，账目保持不变。

默认使用 `log.Default()` 打印每次输入、输出与判定依据，可用
`Ledger.SetLogger` 替换（传 `nil` 关闭）。

### 本地验证

```bash
# 详细日志：查看每条记录的输入、摊入/结转输出与拒绝依据
go test -v ./quota

# 竞态检测（并发 Add/Query）
go test -race -count=3 ./quota

# 覆盖率
go test -cover ./quota
```
