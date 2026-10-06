# 多人实时协作白板：叠放次序与编辑锁服务 — 设计说明

## 1. 范围与模块划分

代码按职责拆成三个包：

- `internal/whiteboard`：生产实现。
  - `errors.go`：拒绝类别（`RejectKind`）、`RejectError`、带持有者与剩余秒数的 `LockError`、`Side`。
  - `treap.go`：隐式键随机平衡树（implicit treap），维护“自底向上”全序，节点带父指针。
  - `board.go`：状态、参数/时钟校验入口、锁判定原语（`effectiveLock` / `purgeExpired` / `touchedLock`）。
  - `ops.go`：`Add / Group / Ungroup / Remove / Reorder`。
  - `locks.go`：`Lock / Unlock`（含同持有者续期）。
  - `query.go`：`Order / Rank / Between / Rev / Snapshot`。
- `internal/naive`：独立朴素模型，全序用 `[]string`，一切操作线性扫描与切片搬移，
  刻意不共享任何数据结构代码，作为差分测试的预言机。
- `internal/diff`：随机差分测试，向两个实现喂同一操作流，逐操作比较错误类别、
  锁细节与完整快照，并把输入、输出、判定依据写入 `testlogs/diff.log`。

并发模型：单个 `sync.Mutex` 串行化所有写操作与查询。
“所有操作可并发调用，结果等价于某个串行顺序”由互斥量直接保证——
每次被接受的操作在其临界区内读到的就是它的“串行位置”上的世界，
因此 Reorder 的三项后置条件（无他人未到期锁、无版本冲突、组成员连续规则）
天然在该串行位置成立。该方案牺牲读操作的并行度，换取可证明的线性化语义；
见第 5 节“被放弃的方案”。

## 2. 关键数据结构

### 2.1 全序：带父指针的 implicit treap

- 每个元素一个 `node`，treap 中序遍历即自底向上的白板次序；节点保存 `left/right/parent/size/prio`。
- 优先级使用受 `Board` 互斥量保护的 `math/rand/v2` PCG 生成，不引入全局随机源竞争。
- 支持：按 0 基位置插入、摘除任意节点、沿父指针求 1 基名次、按名次取节点、区间枚举。
- 摘除策略：把目标按堆性质旋转到叶子再摘除，旋转函数同时维护全部父子指针与子树大小，
  因此摘除后其他节点的指针始终一致，可继续用于 `Rank`。

复杂度（n 为元素数，k 为移动集合大小，k≤200 或为 1）：

- `Rank`：沿父链上行，`O(h)=O(log n)` 期望，**不随 n 线性增长**。
- 单元素 `Reorder`：常数次 treap 摘除/插入，`O(log n)` 期望。
- 组合 `Reorder`：`O(k log n)`；k≤200 为有界常数，仍与 n 无关地保持对数级。
- `Order` 全量导出 `O(n)`（需求只要求全量序列本身）；`Between(lo,hi)` 为 `O(log n + 输出长度)`。

正确性证据：`TestTreapInsertEraseRank` 对 2000 个节点做 4000 次随机插入/删除，
每次操作后校验三条不变量：BST 序、堆序、`size` 与父指针，并将每个节点的
`rankOf` 与切片位置逐一比对。

### 2.2 元素、组合、锁、修订

- `elems map[string]*elem`：元素 → `{group, lastRev}`；另以 `nodes map[string]*node`
  维持标识到 treap 节点的直接指针，使按 id 的摘除不需要先求名次。
- `groups map[string]map[string]struct{}`：组合 → 成员集合；元素与组合共用同一命名空间，
  任何建组/Add 的标识冲突都按“不合法”拒绝。元素的 `group` 字段是成员关系的权威来源，
  集合只用于枚举成员，二者在 Group/Ungroup/Remove 中同步维护。
- `locks map[string]lockRec`：目标 id（元素或组合）→ `{holder, expireAt}`。
  Lock/Unlock 只做至多一次 map 查找/删除，`O(1)`，不随锁总数与修订历史变化。
- `rev` 从 0 开始，每个被接受的 Add/Group/Ungroup/Remove/Reorder 加一；
  每个被影响元素的 `lastRev` 被写成该 rev（整体移动的成员、建/解散组的全体成员、
  Remove 组合时的全体成员都更新——Remove 后元素消失，更新仅体现语义一致性）。
  加解锁与查询不动 rev。

### 2.3 软锁语义

- 有效期为 `[now, now+ttl)`：恰等于到期时刻视为已到期。
  写路径在操作前对“被触碰 id”做惰性清理（`purgeExpired`），查询/快照不做清理，
  只在视图中过滤到期锁，保证查询零写入。
- “触碰一个元素”被拒当且仅当：该元素自身的未到期锁由他人持有，
  或其所属组合上的未到期锁由他人持有（组合锁覆盖全部成员）。
  反之，成员上的锁也会让他人对组合的 Group/Ungroup/Remove/Reorder 失败
  （扫描成员锁即可发现）。
- 同一用户对自己持有的锁再次 `Lock` 是续期，直接用新 `expireAt` 覆盖。
- Reorder 只把移动集合视为“被触碰”；锚点只是位置参照，
  因此把元素放到被他人锁住的锚点旁边不检查、也不被阻止。
- `LockError{Holder, ExpireAt, Remain, TargetID}` 让调用方能区分持有者与剩余秒数
  （`Remain = ExpireAt - now`，被报告时必为正）。

## 3. Reorder 语义与组合连续性

- target 是组合时，成员按其**当前**自底向上相对次序收集为一段（即使此前被别的元素隔开），
  整体摘除后作为一段插入：Below 时整段在锚点正下方，Above 时整段在锚点正上方。
- 锚点属于另一个组合、target 不属于该组合时允许，落点紧贴锚点本身，
  因而可以把该组合从中间隔开；这是规格明确允许的例外。
- 被隔开的组合下次整体移动时仍按成员当前相对次序重新聚成连续段。
- 直接 Reorder 一个仍属于组合的元素返回“不合法”。
- 版本冲突：移动集合中任一元素 `lastRev > baseRev` 即冲突；等于通过。

实现先在“摘除移动元素之后”的剩余序列里重算锚点名次，再按目标侧插入，
移动元素跨过锚点的方向也能得到正确结果（朴素模型用切片拼接给出同一结果，
差分测试对两种方向与跨锚点情形均有覆盖）。

## 4. 拒绝次序（只报第一个）

统一顺序：

1. 参数非法：空 user/id、成员数越界（2..200）、成员重复、ttl∉[1,3600]、now∉[0,10^12]、side 非法、baseRev<0。
2. 时钟回退：`now < lastTs`（只有被接受的写/锁操作推进时钟）。
3. 目标或参照不存在：先查锚点，再查目标（锚点缺失优先报锚点）。
4. 目标与参照不合法：直接移动组合成员、锚点在移动集合内、成员已属组合、id 命名空间冲突等。
5. 被他人持有未到期锁：`LockError` 带持有者与剩余秒数。
6. 乐观版本冲突：`lastRev > baseRev`。

被拒绝的操作在任何状态修改前返回，rev、次序、锁与时钟均不变；
`TestRejectZeroSideEffectDetailed` 对九类拒绝逐一做前后快照对比。

## 5. 关键取舍与被放弃的方案

- **单把互斥量 vs. 分片锁/RMW 乐观并发**：白板操作的判定依赖“次序 + 成员关系 + 锁”
  的全局一致快照，分片锁会让“等价于某个串行顺序”的证明与拒绝次序实现显著复杂化；
  本实现选择单 mutex 的强线性化，所有性能目标（Rank/Reorder 的对数级、Lock/Unlock
  的常数级）都来自数据结构而不是锁的并行度。读多写少的进一步扩展可以在
  `atomic.Pointer[immutableState]` 不可变状态 + RCU 方向演进，接口不变。
- **implicit treap vs. 显式 key 有序结构（跳表/平衡树）+ rank 修正值**：
  显式 key 在 Reorder 时需要重排 key（分数索引会在密集移动后溢出/需要再平衡），
  treap 天然以位置为键，整段摘除/插入只动 `O(k log n)` 个节点。
  曾考虑带“子树大小”的 splay，但 splay 的名次查询会改写结构，与读路径持只读锁的
  预期不符，故放弃。
- **父指针求名次 vs. 每次从根按 size 搜索**：后者需要 id→节点后还要从根重新定位，
  仍可做到 O(log n)，但父指针让 Rank 只沿一条上行链完成，缓存行为更直观；
  代价是旋转/摘除代码必须维护父指针，已用随机不变量测试覆盖。
- **锁记录保留 vs. 到期立即清除**：放弃后台清扫线程（引入并发与生命周期问题），
  改为“写路径惰性删 + 读路径过滤”，Lock/Unlock 依旧 O(1)。
- **Remove 组合成员的处理**：元素属于组合时禁止单独删除（返回“不合法”），
  必须 Remove 整个组合或先 Ungroup。这样“成员只能作为组合整体被操作”的规则
  在 Reorder 与 Remove 上保持一致。

## 6. 本地验证方法

需要 Go 1.26+；如 `go` 不在 PATH：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认构建缓存目录只读时需要
```

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 指定场景
go test ./internal/whiteboard/ -run 'TestLockExpiryExactAndOneSecondBefore|TestVersionConflictBoundary' -v
go test ./internal/diff/ -run TestRandomDifferential -v

# 两档规模（1e3 与 1e5，相差两个数量级）复杂度对照
go test -run '^$' -bench 'Benchmark(Rank|SingleReorder|LockUnlock)' -benchtime=100000x ./internal/whiteboard/

go vet ./...
gofmt -l internal/
```

实测（linux/arm64，`testlogs/bench.txt`）：

| 操作 | N=1,000 | N=100,000 | 结论 |
| --- | --- | --- | --- |
| Rank | 94.9 ns/op | 155.2 ns/op | 规模 ×100，耗时 ×1.6，符合 O(log n)，非线性 |
| 单元素 Reorder | 777.3 ns/op | 923.4 ns/op | ×100 规模仅 ×1.19，符合 O(log n) |
| Lock/Unlock（无背景锁） | 260.5 ns/op | 304.4 ns/op | 几乎不变 |
| Lock/Unlock（预置 N/2 把他人锁） | 132.4 ns/op | 100.1 ns/op | 锁数 ×100 不增长，O(1) |

（绝对数值随机器波动；关键证据是 100 倍规模下的增长倍数远小于 100，
且 Rank/Reorder 的倍数与 log₂(100)≈6.6 同量级而锁操作不增长。）

### 测试覆盖对照

- 到期恰等于 vs. 差一秒：`TestLockExpiryExactAndOneSecondBefore`
- 续期与他人加锁：`TestLockRenewal`、`TestLockExpiryExactAndOneSecondBefore`
- 组合整体移动保持内部次序：`TestReorderSingleAndGroup`、`TestConcurrentGroupMove`
- 锚点在他人组合内（允许隔开）：`TestAnchorInsideOtherGroup`
- 成员单独移动被拒：`TestReorderSingleAndGroup`
- 拒绝次序相邻类别对：`TestRejectPrecedenceAllAdjacentPairs`
- 被拒绝操作零副作用：`TestRejectZeroSideEffectDetailed`、`TestClockMonotonicAndZeroSideEffect`
- 版本边界（等于通过、大于冲突）：`TestVersionConflictBoundary`
- 朴素模型对照 ≥1500 组随机序列：`internal/diff`（1500 条、每条 60 个操作，
  日志 `testlogs/diff.log`，逐行打印 IN / OUT / JUDGE）
- 并发等价性：`TestConcurrentConsistency`、`TestConcurrentGroupMove`（`-race` 下通过）
- treap 结构不变量：`TestTreapInsertEraseRank`

## 7. API 速览

```go
b := whiteboard.New()
b.Add(user, id, now)
b.Group(user, groupID, ids, now)
b.Ungroup(user, groupID, now)
b.Remove(user, id, now)
b.Reorder(user, target, anchor, whiteboard.Above /*或 Below*/, baseRev, now)
b.Lock(user, id, ttl, now)
b.Unlock(user, id, now)
b.Order()                // []string 自底向上
b.Rank(id)               // 1 基名次，O(log n)
b.Between(lo, hi)        // 名次闭区间
b.Rev()
```

错误处理：用 `errors.As` 区分 `*whiteboard.RejectError`（取 `.Kind`）
与 `*whiteboard.LockError`（取 `.Holder/.Remain/.ExpireAt/.TargetID`）。
