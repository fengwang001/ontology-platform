# Percolator 式分布式事务锁解析器

`percolator` 包实现了一个可并发调用、结果确定可复现的 Percolator 风格两阶段
事务引擎（单机内存版，一把全局互斥锁保证等价串行）。代码入口为 `Store`
（`store.go`），类型定义见 `types.go`。

## 数据结构

- 全局时间戳 oracle：初值 0；`Begin` 与 `CommitPrimary` 每领取一次严格加一。
  因此任何已提交版本的提交时间戳都严格大于其开始时间戳。
- 每个键包含两部分：
  - 已提交版本表：每条记录为 `(CommitTs, StartTs, Type, Value)`，`Type`
    为 `Put`、`Delete` 或 `Rollback`。
  - 至多一把锁：`(StartTs, Primary, Expire, Type, Value)`，其中
    `Expire = Prewrite 接受时的 now + ttl`。
- 事务状态：刚 `Begin`（started）→ 已预写（prewritten）→ 已提交
  （committed，记下主键领取的 commitTs）或已回滚（aborted）。
- 时钟水位：所有带 `now` 的操作要求 `0 <= now <= 10^15` 且不小于已接受操作
  的最大 `now`；操作成功后水位推进，被拒绝则不推进。

## 两阶段写入与检查次序

`Prewrite(st, muts, primary, ttl, now)` 的错误只报第一个，顺序为：

1. 参数非法：`muts` 为空或含空键/重复键、变更类型不是 Put/Delete、
   `primary` 不在 `muts` 中、`ttl` 不在 `[1, 10^9]`。
2. 事务不存在：`st` 从未被 `Begin` 领取。
3. 状态非法：事务已经 Prewrite 过（每个事务只允许一次）。
4. 时间非法：`now > 10^15`。
5. 时钟回退：`now < 水位`。
6. 按 `muts` 顺序逐键检查，键内优先级固定：
   - 该键已有 `StartTs == st` 的 Rollback 版本 → 已回滚；
   - 存在其他事务的锁（`StartTs != st`）→ 键被锁；
   - 存在 `CommitTs > st` 的 Put/Delete 版本（Rollback 永远不算）→ 写冲突。

全部键检查通过后才一次性给每个键加锁；任意一键失败，整批不留锁、不改任何
状态（原子预写）。

提交阶段：

- `CommitPrimary(st)`：错误顺序为「事务不存在 → 状态非法（非已预写）→
  主锁丢失」。成功时领取 commitTs，把主键的锁替换成
  `(commitTs, st, 类型, 值)`，事务置为已提交。
- `CommitKeys(st, keys)`：错误顺序为「事务不存在 → 状态非法（未提交）→
  参数非法（`keys` 不是本事务写集的子集、空键、重复键）」。仍持有 `st`
  锁的键用主键那同一个 commitTs 提交；锁已不在的键跳过。
- `Abort(st)`：错误顺序为「事务不存在 → 状态非法（非已预写）」。清除本
  事务仍持有的全部锁，并在每个写集键上写且只写一条 Rollback 版本
  `(CommitTs=st, StartTs=st)`。读者已经回滚过的键不重复写，且读者回滚
  不改变事务状态，所以被读者回滚后的事务仍可 `Abort`。

## 读者遇锁的解析规则

`CheckTxnStatus(primary, st, now)` 的错误顺序为「参数非法（primary 为空、
st<1）→ 时间非法 → 时钟回退」。通过校验后查主键：

- 主键上有 `st` 的锁：`now >= Expire`（到期恰等于 now 即过期）则删锁、写
  Rollback、返回已回滚；否则返回存活，不改任何状态。
- 无该锁但有 `StartTs == st` 的版本：Put/Delete 返回已提交及该版本的
  commitTs；Rollback 返回已回滚。
- 锁与版本都没有：写入一条保护性 Rollback（`CommitTs=st`）并返回已回滚。

`Get(key, rts, now)` 的错误顺序为「参数非法（key 为空、rts 为负）→ 时间
非法 → 时钟回退」，之后才可能报被锁：

- 键上锁的 `StartTs <= rts`（开始时间戳恰等于 rts 也阻塞；大于 rts 的锁
  对该读者不可见、完全不处理）时，以锁上的主键与开始时间戳调用
  `CheckTxnStatus`：
  - 已提交：把该键的锁以前滚方式写成主键的 commitTs 的版本（不领取新
    时间戳）；
  - 已回滚：删锁并写 Rollback，然后继续读旧版本；
  - 存活：返回被锁错误，状态不变。
- 解析后返回 `CommitTs <= rts` 的最新 Put/Delete 版本；Rollback 永不被
  读到；Delete 与无可见版本都返回「不存在」。

## 原子可见性

已提交事务所有键的版本共享主键的 commitTs。对任意读时间戳 rts：
`rts < commitTs` 时该事务一个键都不可见；`rts >= commitTs` 时读者会把残留
的次要锁逐个前滚到同一个 commitTs，于是要么全可见、要么全不可见，不会出现
只见 a 不见 b。

## 保护性回滚记录

当 `CheckTxnStatus` 在主键上既找不到 `st` 的锁也找不到 `st` 的版本时
（典型场景：事务 Begin 后从未成功预写，或主锁已被清掉但没留下任何版本），
写入 `(st, st, Rollback)`。它有两个作用：一是给后来的读者一个确定的
「已回滚」答复；二是阻止该事务迟到的 Prewrite——预写检查会先发现这条
Rollback 并报已回滚，从而不会在主键状态已被判定之后又冒出新锁。

## 并发与确定性

所有操作在同一把互斥锁下完成校验与状态变更，因此任何并发交织都等价于某个
串行顺序；结构上保证每个键任何时刻至多一把锁。被拒绝的操作在任何状态变更
之前返回，不改动锁、版本、事务状态、oracle 与时钟水位。oracle 只由成功的
`Begin`/`CommitPrimary` 推进，故相同操作序列重放得到完全相同的版本表、
锁与返回值。

## 测试与本地验证

测试文件：

- `store_rules_test.go`：规则定点用例，覆盖开始时间戳恰等于/大于 rts 的
  阻塞边界、到期恰等于 now 回滚、`CommitTs > st` 的写冲突边界、Rollback
  不触发冲突、三种逐键错误的优先级、整批预写原子性、前滚沿用主键
  commitTs、部分提交后各键一致可见、保护性 Rollback 阻止迟到预写、主锁
  丢失提交失败、Abort 每键留一条 Rollback、被拒绝不改状态，以及规格中的
  完整示例。
- `naivemodel_test.go`：按上述规则逐步直写的独立朴素模型，与生产实现无
  共享代码。
- `diff_test.go`：2000 组随机事务/读写序列，逐步比对两实现的返回值与全量
  快照（版本表、锁、oracle、水位），并在测试日志中打印每组输入、输出与
  判定依据（`go test -v` 可见）。
- `concurrency_test.go`：`-race` 下的多写者/多读者压力测试与相同序列
  确定性重放比对。

运行方式：

```bash
# 普通全量测试
go test ./...

# 竞态检测 + 详细输入/输出日志
go test -race -v ./percolator

# 只跑 2000 组朴素模型对照
go test -run TestRandomDifferential -v ./percolator

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```
