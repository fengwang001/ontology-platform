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

## 周期预算账本（`budget` 包）

按周期重置的用量预算账本：先预留额度，再按实际用量结算或释放。
时间由调用方以逻辑时钟 `now` 传入，便于测试与重放。

### 周期归属与过期规则

- 主体注册时给出周期预算 `Q`、周期长度 `P`、过期期限 `X`（均须为正整数，否则拒绝注册）。
- 周期号为 `floor(now / P)`，是固定长度 `P` 的连续区间，各周期独立核算。
- 预留与已用量都记入**预留时刻**所在的周期：结算（无论多迟）把实际用量 `u`
  记入预留发生时的周期，而不是结算时刻的周期，因此不占新周期额度。
- 预留在「预留时刻 + X」起过期（`now >= 预留时刻 + X`，**恰到点即过期**），
  过期后不再占用额度，但仍可结算。
- 预留 `r`（正整数）成功当且仅当该周期 `已用量 + 在途预留 + r <= Q`，
  成功返回全局顺序生成的预留号（`v1`、`v2`……）。

### 透支处理

- 结算时 `u` 超过 `r` 的部分照常记入该周期已用量，可使已用量超过 `Q`（透支）。
- 透支后该周期内新预留一律被拒，额度不足时返回剩余量（透支时报 0）。
- 显式释放使在途预留不再占额度，此后不可再结算；已结算的预留不可释放。

### 错误优先级

被拒绝的操作不改变任何账目，且按以下顺序只报第一个原因：

- 注册：`Q/P/X 非正`（`INVALID_PERIOD_CONFIG`）、主体已注册（`SUBJECT_ALREADY_REGISTERED`）。
- 预留：主体未注册（`SUBJECT_NOT_REGISTERED`）→ `r` 非正（`NON_POSITIVE_AMOUNT`）
  → 额度不足（`INSUFFICIENT_QUOTA`，附剩余量，透支时为 0）。
- 结算：预留号不存在（`RESERVATION_NOT_FOUND`）→ 已结算（`ALREADY_SETTLED`）
  → 已释放（`ALREADY_RELEASED`）→ `u` 为负（`NEGATIVE_USAGE`）。
- 释放：预留号不存在 → 已结算 → 已释放。

### 并发语义

所有操作由单把互斥锁串行化，预留、结算、释放与查询可安全并发调用：
任意交错下每次预留成功的瞬间 `已用量 + 在途预留 <= Q`；
同一预留只能被结算或释放其一且至多一次；
查询任一周期的已用量与在途量与逐笔朴素记账一致（由随机对拍测试验证）。

### 本地验证

```bash
# 全部测试（日志打印输入、输出与判定依据）
go test -v ./budget

# 竞态检测
go test -race ./budget

# 指定场景
go test -run TestConcurrentReserveNeverExceeds ./budget
```
