# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## freeze 包：司法冻结与轮候冻结管理器

`freeze` 包（`freeze/freeze.go`）实现银行账户的司法冻结与轮候冻结管理，
入口为 `freeze.NewManager()`，提供 `Deposit` / `Debit` / `Freeze` /
`Unfreeze` / `Seize` / `SeizeQ` / `Query` 七个方法。

### 有效冻结额公式

账户状态为余额 `B` 与按登记先后排列的冻结令队列，每道令有名义金额 `a`
与到期时刻 `exp`（0 表示永不到期）。有效冻结额不单独存储，只是 `B` 与
队列的纯函数，从队首起逐令计算：

```
e_i = min(a_i, max(0, B - 前面各令有效冻结额之和))
V   = B - 全部 e_i 之和        （可用余额）
```

因此 `a` 可以大于当前余额，超出部分即轮候，随后续存入、前序令解除或
前序令失效而递补生效；任一道令的 `e_i` 只由 `B` 与排在它前面的令决定。

### 到期失效规则

时刻 `t` 的有效状态定义为：先把队列中 `0 < exp <= t` 的令全部移出
（`exp` 恰等于 `t` 即已失效），再计算各 `e_i`。每次操作都先在时刻 `t`
的有效状态上判定，判定通过才把失效清理与操作一并生效；被拒绝的操作不
改变任何状态（含最大时刻 `m` 与失效的令）。已失效的令编号可以再次使用。

### 解冻与扣划的口径区别

- `Unfreeze(acct, t, id, x)`：司法解冻，只减少名义金额 `a_i`（要求
  `1 <= x <= a_i`），不改变余额 `B`；`a_i` 减为 0 则整令移出队列。
- `Seize(acct, t, id, x)`：对单令的司法划扣，要求 `1 <= x <= e_i`
  （不得超过该令当前有效冻结额），令 `B` 与 `a_i` 各减少 `x`，
  `a_i` 减为 0 则整令移出。

### SeizeQ 的次序规则

`SeizeQ(acct, t, x)` 为按队列顺序的总额划扣，是一个原子步骤：先判定
`1 <= x <= 全部 e_i 之和`（不满足则整体拒绝，不得部分执行），再按开始
时的 `e_i` 快照从队首起逐令扣 `min(e_i, 剩余待扣量)`，令 `B` 减少 `x`、
各令 `a_i` 各减少所扣之数，`a_i` 减为 0 的整令移出。

### 时序与错误码

所有操作与查询都带时刻 `t`（0 到 10^12），`t` 不得小于全部账户已接受
操作与查询的最大时刻 `m`（同刻可多次）。被拒绝的原因按如下优先级只报
第一个，并以 `*freeze.Error` 的 `Code` 字段区分：

1. `ErrInvalidParam`：空编号、金额/exp/t 越界、存入后 `B` 超过 10^15；
2. `ErrTimeRegression`：`t` 小于 `m`；
3. `ErrAccountNotFound`：账户不存在（`Deposit` 除外）；
4. `ErrDuplicateOrder` / `ErrOrderNotFound`：令编号重复（失效清理之后
   判定）或令不存在（已失效的令也算不存在）；
5. `ErrUnfreezeExceeds` / `ErrSeizeExceeds` / `ErrSeizeQExceeds` /
   `ErrInsufficientAvail`：解冻超额、扣划超额、可用不足。

所有方法可并发调用（内部互斥串行化），结果等价于某个串行顺序；
任意有效状态下 `sum(e_i) <= B`、`V >= 0`、`0 <= e_i <= a_i`，且全部
余额之和加已扣划与已 `Debit` 之和恒等于全部存入之和。

### 本地验证

```bash
# 全部单测（例一/例二完整序列、各边界用例、2000 组随机序列对照朴素模拟）
go test ./freeze/

# 查看随机对照的输入、输出与判定依据日志
go test ./freeze/ -run TestRandomAgainstNaive -v

# 并发与竞态检测
go test -race ./freeze/
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
