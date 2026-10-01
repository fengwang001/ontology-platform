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

## 用量预算账本（`budget` 包）

按周期重置的用量预算账本：先预留额度，再按实际用量结算或释放。
核心实现见 `budget/budget.go`，测试见 `budget/budget_test.go`。

### 周期归属与过期规则

- 注册：`Register(name, Q, P, X)`，Q（周期预算）、P（周期长度）、X（过期期限）均须为正，否则拒绝。
- 周期为固定长度 P 的连续区间，周期号 = `t / P` 向下取整，各周期独立核算。
- 预留 `Reserve(name, t, r)` 记入时刻 t 所在周期；成功当且仅当该周期
  `已用量 + 在途预留 + r <= Q`，成功返回全局顺序预留号 `v1、v2……`。
- 预留在 `预留时刻 + X` 起过期（恰到点即过期，`t >= 预留时刻 + X`），
  过期后不再占额度，但仍可结算。
- 结算 `Settle(id, u)`：在途或已过期的预留都接受，u 记入**预留发生时**的周期，
  而非结算时刻的周期（迟到结算不占新周期额度）。
- 释放 `Release(id)`：在途预留不再占额度，此后不可再结算。

### 透支处理

结算时 u 超过预留额 r 的部分照常记入，可使该周期已用量超过 Q；
此后该周期内新预留一律被拒（剩余量按 0 上报），其他周期不受影响。

### 错误优先级

被拒绝的操作不改变任何账目，原因可用 `errors.Is` / `errors.As` 区分：

- 注册：Q、P、X 任一非正 → `ErrNonPositiveParam`。
- 预留（只报第一个）：`ErrSubjectNotRegistered` → `ErrNonPositiveAmount` →
  `InsufficientQuotaError`（携带 `Remaining` 剩余量，透支时为 0）。
- 结算（只报第一个）：`ErrReservationNotFound` → `ErrAlreadySettled` →
  `ErrAlreadyReleased` → `ErrNegativeUsage`。
- 释放（只报第一个）：`ErrReservationNotFound` → `ErrAlreadySettled` →
  `ErrAlreadyReleased`。

### 并发与查询

所有操作由互斥锁保护，可并发调用：任意交错下每次预留成功的瞬间
`已用量 + 在途预留 <= Q`，同一预留只能被结算或释放其一且至多一次。
`Query(name, period, t)` 返回该周期在时刻 t 的已用量与在途量
（在途量只统计 t 时刻未过期的预留），与逐笔朴素记账一致。

### 本地验证

```bash
# 全部测试（含竞态检测），-v 可查看每笔输入、输出与判定依据日志
go test -race -v ./budget/

# 单个场景，如并发预留不超额
go test -race -run TestConcurrentReserveNeverExceedsQuota -v ./budget/
```
