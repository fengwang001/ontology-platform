# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 司法冻结与轮候冻结管理器（`freeze` 包）

`freeze.Manager` 在账户余额内按冻结令登记先后逐令占用冻结额，原生支持
轮候冻结、到期失效、按队列顺序的司法扣划。所有方法以单一互斥锁串行化，
并发调用等价于某个串行顺序，`SeizeQ` 是一个原子步骤。

### 有效冻结额公式

时刻 `t` 的有效状态：先把队列中满足 `0 < exp <= t` 的令全部移出
（`exp == t` 即已失效；`exp == 0` 永不到期），再从队首起逐令计算

```
e_i = min(a_i, max(0, B - sum(e_j, j < i)))
```

可用余额 `V = B - sum(e_i)`。`e_i` 只是 `B` 与队列的纯函数、不单独存储，
只取决于 `B` 和排在前面的令，与后面的令无关；名义金额超出当前可冻余额的
令即轮候令，随存入、前序令解除或前序令失效自动递补生效。

### 操作口径

- `Deposit(acct, t, x)`：账户不存在则创建，`B += x`（存入后 `B <= 1e15`）。
- `Debit(acct, t, x)`：要求 `x <= V`，`B -= x`（普通借记，不动冻结令）。
- `Freeze(acct, t, id, a, exp)`：追加队尾；`exp == 0` 永不到期，否则
  `t < exp <= 1e12`。编号在当前队列内唯一，已移出/已失效的编号可复用。
- `Unfreeze(acct, t, id, x)`：口径是**名义金额**，要求 `1 <= x <= a_i`，
  `a_i -= x`，减为 0 整令移出；不动 `B`。
- `Seize(acct, t, id, x)`：对单令的司法扣划，口径是**有效冻结额**，
  要求 `1 <= x <= e_i`，`B` 与 `a_i` 同减 `x`，`a_i` 为 0 整令移出。
  与 Unfreeze 的区别：Unfreeze 只解除名义冻结（可含尚未生效的轮候部分），
  Seize 只能划扣当前真实冻结住的钱，且钱被实际划走（`B` 减少）。
- `SeizeQ(acct, t, x)`：按队列顺序的总额扣划。要求
  `1 <= x <= sum(e_i)`，**先判后改、不部分执行**；以开始时的 `e_i`
  快照从队首起逐令扣 `min(e_i, 剩余待扣量)`，`B -= x`，各令
  `a_i -= 所扣之数`，`a_i` 为 0 整令移出。
- `Query(acct, t)`：返回 `B`、`V` 与各令的 `id / a / e / exp`。

### 时刻、拒绝与不变量

- 所有 `t` 满足 `0 <= t <= 1e12`，且不得小于已接受操作/查询的最大时刻
  `m`（同刻可多次调用）。每次操作先在 `t` 的有效状态上判定，判定通过才把
  失效清理与操作一并生效；被拒操作不改变任何状态（含 `m` 与失效令）。
- 拒绝原因按优先级只报第一个：参数非法 → 时序倒退 → 账户不存在
  （Deposit 除外）→ 令编号重复 / 令不存在（失效令算不存在）→
  解冻超额 / 扣划超额（Seize、SeizeQ）→ 可用不足（Debit）。
- 恒常不变量：`sum(e_i) <= B`、`V >= 0`、`0 <= e_i <= a_i`；
  `sum(各账户余额) + 累计扣划 + 累计 Debit == 累计存入`；同序列重放
  结果完全一致。错误以 `*freeze.Error` 返回，用 `freeze.IsError(err, reason)`
  判别 `Reason`。

### 本地验证

```bash
# 全量测试（含例一/例二、边界条件、2000 组随机对照、竞态检测）
go test -race -v ./freeze

# 只看随机对照日志（输入、输出与判定依据）
go test -v ./freeze -run TestRandomDifferential

# 全仓构建/检查
go build ./...
go vet ./...
gofmt -l .
```

说明：若默认 `GOCACHE` 位于只读文件系统，可设置 `GOCACHE=/tmp/gocache`。
随机对照以独立于实现的朴素模型（按规格逐令转录）比对接受/拒绝原因与
每次 `Query` 的完整快照。

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
