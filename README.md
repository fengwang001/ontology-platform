# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Percolator 分布式事务锁解析器

`percolator` 包实现了一个内存版的 Percolator 式两阶段事务模型，
入口见 `percolator/percolator.go`。所有方法都持有同一把互斥锁，
因而并发调用等价于某个串行顺序；被拒绝的操作不改任何状态。

### 数据结构

- 全局时间戳 oracle 初值 0，`Begin` 与 `CommitPrimary` 各领取一次并加一。
- 物理时钟水位 `water`：带 `now` 的操作要求 `0 <= now <= 10^15`，
  且 `now` 不小于已接受操作的最大 `now`，成功后推进水位。
- 每个键有：
  - 版本表 `[]Version{CommitTS, StartTS, Kind, Value}`，按 commitTs 降序
    存放；`Kind` 为 `Put`、`Delete` 或 `Rollback`。Rollback 记录的
    commitTs 与 startTs 相同，永不被 `Get` 读到。
  - 至多一把锁 `Lock{StartTS, Primary, Deadline, Kind, Value}`。
- 每个事务记录状态机：`fresh → prewritten → committed/aborted`。

### 两阶段写入的检查次序

`Prewrite(st, muts, primary, ttl, now)` 的报错顺序为：
参数非法（空批、键重复、空键、primary 不在批中、ttl 越界、类型非法）
→ 事务不存在 → 状态非法（必须是刚 Begin 且从未 Prewrite）→ 时间非法
→ 时钟回退 → 逐键检查。逐键按 `muts` 顺序、每键内部按
「该键已有 startTs==st 的 Rollback（已回滚）→ 其它事务的锁（被锁）
→ commitTs > st 的 Put/Delete（写冲突，Rollback 不算）」判定，
只报第一个。全部键通过后才整体原子加锁（到期 `now+ttl`），
因此后面的键冲突时前面的键不会留下锁，也不会创建空键。

- `CommitPrimary(st)`：事务须已预写未提交，主键锁须仍属于 st，
  否则报 `ErrLockLost`；领取 commitTs，把主键锁换成正式版本。
- `CommitKeys(st, keys)`：事务须已提交，`keys` 必须是本批 muts 的
  子集；仍持有 st 锁的键用同一个 commitTs 提交，锁已不在则跳过。
- `Abort(st)`：已预写未提交时删除仍持有的锁，并在每个键写
  `(st, st, Rollback)`（已存在则不重复）。读者驱动的回滚不改事务
  状态，所以被读者回滚的事务仍可 `Abort`。

### 读者遇锁的解析规则

`Get(key, rts, now)`：仅当键上锁的 `startTs <= rts` 时阻塞读取，
以锁记录的 primary/startTs 调 `CheckTxnStatus`：

- 主键有 st 的锁：`now >= Deadline`（到期恰等于 now 即回滚）则删锁
  并写 Rollback，返回已回滚；否则返回存活，`Get` 报 `ErrKeyLocked`
  且不改状态。
- 主键无锁：有 startTs==st 的 Put/Delete 版本则返回已提交及
  commitTs；有 Rollback 则返回已回滚；两者都没有则写保护性 Rollback
  并返回已回滚。
- 已提交则把当前键的锁用主键的 commitTs 前滚（不分配新时间戳）；
  已回滚则删当前键锁并写 Rollback。
- 解析后返回 commitTs <= rts 的最新 Put/Delete；Delete 与无版本都
  视为不存在，Rollback 永不被读到。

### 保护性回滚记录

当 `CheckTxnStatus` 对一个「无锁且无任何版本」的 (primary, st) 被调用
时，写入 `(commitTs=st, startTs=st, Rollback)`。它不代表事务真的写过
数据，只用于在该事务迟到 Prewrite 时稳定地判定为已回滚，从而让
「先检查、后预写」的竞态得到确定、可复现的结果。

### 原子可见性

主键先提交确定唯一 commitTs，次要键要么被提交者用同一 commitTs 提交，
要么由读者依据主键状态前滚/回滚。因此对任意读时间戳，一个已提交
事务的全部键要么都可见（版本 commitTs 相同），要么都不可见。

### 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素参考模型差分对照）
go test ./percolator -v

# 竞态检测 + 全量
go test -race ./...

# 仅跑 2000 组差分（seed 1..2000，日志打印每个输入、输出与判定依据）
go test ./percolator -run TestDifferential2000 -v
```

测试文件：

- `percolator/percolator_test.go`、`percolator/rules_test.go`：
  规格走查与逐规则用例（rts 边界、到期边界、写冲突边界、回滚不
  冲突、报错优先级、整批原子、前滚 commitTs、部分提交一致性、
  保护性回滚、主锁丢失、Abort 留 Rollback、被拒绝不改状态）。
- `percolator/naive_test.go`：独立朴素实现，作为差分基准。
- `percolator/diff_test.go`：随机生成事务与读写序列，逐步比对真实
  实现与朴素实现的返回值、错误及完整版本表/锁快照。
- `percolator/concurrent_test.go`：高并发压力与跨键原子可见性不变量。

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
