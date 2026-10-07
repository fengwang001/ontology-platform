# 乐观重试下的链接基数校验 — 设计说明

## 1. 问题与约束

对象实例可在某链接类型（区分出向/入向）上声明基数上限。一次针对该实例的
更新以乐观方式提交；当调用方基线落后时，请求可在内部有限次重试。
需求的要害是：

1. 每次重试必须基于**重试当时**最新读取到的关联集合重新计算基数，
   不得复用第一次尝试读到的集合或计数。
2. 三类拒绝原因互斥，且判定顺序固定：版本冲突 > 基数不满足 > 重试耗尽。
3. 失败的中间尝试在外部可观察状态上必须“像没有发生过”；只有最终成功
   的一次提交才推进版本、关联与逻辑时钟。
4. 并发争抢同一唯一名额时恰好一方成功；不能重复占用，也不能名额空置
   却全员失败。
5. 重试次数有硬上限，竞争再激烈也不能无限重试（无长期饥饿）。
6. 单次基数判定开销不随现有关联总数增长；证据不依赖额外对外暴露状态。
7. 任意并发历史的最终关联集合 + 各调用结果，必须等价于某种串行执行。

## 2. 总体方案

实现位于 `ontology` 包（`types.go` / `store.go` / `submit.go` / `errors.go`），
`cmd/server` 是最小 HTTP 演示。

### 2.1 数据结构

- `Store`：对象表 `map[string]*objectState` + 单调逻辑时钟 `clock`
  + 仅追加的成功提交记录 `commitLog`（仅用于测试重放，不参与任何判定）。
- `objectState`：
  - `links map[约束键]set(对端ID)` —— 当前实际持有的全部关联；
  - `limits map[约束键]上限` —— 该实例上声明的基数约束；
  - `version int64` —— 每次成功提交 +1；
  - `mu sync.Mutex` —— **每个实例一把锁**。

约束键为 `"out:"+linkType` / `"in:"+linkType`。

### 2.2 单次尝试：读-复核-校验-提交在同一把实例锁内原子完成

`Submit` 的每次迭代（`Store.attempt`）都重新进入实例临界区：

1. 锁内读取当前 `version` 与全部关联（权威快照，完整拷贝进轨迹）。
2. **版本冲突优先**：当前版本 != 本次尝试的期望版本 → 立即以
   `ReasonVersionConflict` 结束本次尝试，不做基数判定，不写任何状态。
3. 否则在锁内最新关联上重新计算本次变更涉及的**每一个**约束
   （`projectViolations`）：任一投影结果越界 → `ReasonCardinality`，
   逐个返回 `Violation{Current, Projected, Max}`，多约束分别列出、
   不混同；不写任何状态。
4. 全部满足才真正应用 ops、`version++`、推进全局逻辑时钟并追加提交记录。

因为第 1–4 步之间不释放实例锁，“读到的状态”和“据此做的判定/提交”
锚定在同一个线性化时点：不存在“读了旧集合却提交”的窗口。

### 2.3 期望版本（基线）如何在重试间更新

- 第 1 次尝试的期望版本 = 调用方提供的 `BaseVersion`。
- 此后每次尝试开始时，先在锁外**新鲜读取**当前快照（`Store.Snapshot`），
  以刚读到的版本作为本次期望版本；前一次尝试读到的关联集合/计数
  只写入轨迹，绝不参与本次判定。
- 两次读取之间（新鲜读 → 进入实例锁复核）若有他人提交，版本被推进，
  本次尝试在第 2 步判版本冲突并重试。这正是乐观并发控制的标准形态，
  也保证“每次重试读到的基数若与上次不同，以本次为准”。

### 2.4 三类互斥结果与判定顺序

单次尝试按 2.2 的顺序只命中一种原因；请求级结果：

- 成功：`result.Committed=true`，`err=nil`。
- 基数不满足：版本匹配但最新读取下越界 → 立即终止，返回
   `ErrCardinality`（即使还有剩余重试预算——竞争不会改变“当前已满”
   这一事实，继续重试只会掩盖业务拒绝）。
- 版本冲突且仍有预算：重试。
- 版本冲突且预算已尽：
  - `MaxAttempts==1`：调用方基线即落后、根本没进入过重试，返回
    `ErrVersionConflict`（语义上是“请重新读后再发请求”）；
  - `MaxAttempts>1`：请求已在重试中被连续抢先至预算耗尽，返回
    `ErrRetriesExhausted`，错误信息明确“不是基数不满足”。

因此“基数优先于耗尽”由最后一次尝试的顺序保证：锁内若版本匹配则
一定先做基数判定；只有版本不匹配时才可能走到耗尽。

### 2.5 失败尝试的不可观察性

版本冲突路径与基数违例路径都在任何写操作之前 `return`；不推进
`version`、不改 `links`、不推进 `clock`、不追加 `commitLog`。
测试 `TestFailedAttemptsAreInvisible` 与 `TestRetryBudgetExactlyExhausted`
直接断言：N 次失败尝试后时钟增量恰好等于他人合法提交次数。

### 2.6 并发正确性：恰好一方成功 + 可串行化

同一实例的所有尝试在同一把 `objectState.mu` 上串行；每次提交是
“复核版本 + 基数判定 + 写入”的原子临界区。因此：

- 唯一名额：两个请求不可能同时观察到“尚有名额”并各自写入——
  后入临界区者要么版本已变（冲突重试），要么在最新关联上看到名额已满
  （基数拒绝）。`TestConcurrentSingleSlotExactlyOneWinner` 在 25 轮
  32 路并发下断言 winners 恰为 1、版本恰推进 1 次。
- 可串行化：成功提交按全局逻辑时钟形成全序；以该全序作为串行顺序，
  把所有未成功请求排在其后，在朴素串行模型中它们等价于
  “名额已满后到达”的基数拒绝。`TestRandomConcurrentEquivalentToNaiveSerial`
  对 30 个随机种子（40 请求、容量 7、预算 6）逐请求比对成功集合、
  最终关联集合与分类。

### 2.7 无长期饥饿

预算为每请求 `MaxAttempts` 次尝试；每次尝试无论成功失败都终止循环
或消耗一次计数。后来者可以连续抢先，但任一请求被抢先次数达到预算
即在当次尝试后得到明确的“耗尽”判定。`TestBoundedRetriesNoStarvation`
在 8 路持续竞争背景流上验证尝试次数严格不超过预算且必然收敛。

### 2.8 O(1) 基数判定与证据形式

判定函数 `projectViolations` 只遍历本次 ops 触及的不同约束键：

- 当前数量 = `len(links[key])`（Go map 长度读取，O(1)）；
- 每条 op 对其键做一次存在性查询，O(1)。

不维护额外计数器，因而**不存在需要对外暴露或保持一致的额外状态**；
证据来自代码结构本身与基准：

```
go test ./ontology/ -bench=BenchmarkProjectViolations -run='^$' -benchmem
existing=10      ~110 ns/op
existing=100000  ~118 ns/op   # 关联数扩大一万倍，判定耗时基本不变
```

说明：规格同时要求每次尝试完整重读并记录“全部链接关联”（供重放），
该读取/拷贝本身是 O(当前关联数) 的 I/O；这里 O(1) 特指**基数判定的
计算开销**，二者在 `projectViolations` 与 `snapshotLocked` 中明确分离。

## 3. 可观测性 / 完整轨迹

`Result.Attempts []AttemptDecision` 对每次内部尝试记录：
尝试序号、该次锁内权威读取快照（版本 + 每个约束键的计数 + 完整关联集）、
命中原因、以及基数违例时逐项依据。它支撑：

- “没有复用旧计数”的审计（相邻尝试快照可不同且各自完整）；
- 重放核验：`TestDeterministicInterleavingReplay` 用轨迹中的版本序列
  重建尝试级调度表，交给独立的朴素串行重试模型 `runRetry` 重放，
  逐尝试比对 conflict/cardinality/committed/exhausted。

## 4. 关键取舍与被放弃的方案

- **放弃“锁外先判基数、提交时只 CAS 版本”**：CAS 成功与基数校验之间
  仍需保证原子性，否则会出现两名请求同时通过基数检查。本方案把
  复核+校验+写入收进同一把实例锁，代价是同实例写完全串行；对本体
  元数据这类竞争粒度，这是换取正确性与简单性的合理代价。跨实例仍并行。
- **放弃维护独立计数字段**：额外计数器需要在每条增删路径上严格同步，
  且属于“额外状态”。直接用链接集合的 `len` 既 O(1) 又不可能与事实漂移。
- **放弃“基数满也继续重试”**：版本匹配时基数满是确定业务事实，
  继续重试只会把业务拒绝伪装成耗尽；立即返回更利于调用方区分处理。
- **放弃无界/自适应退避**：规格要求有限预算且无饥饿；固定
  `MaxAttempts` 语义最可验证。没有引入 sleep（也避免时钟被失败尝试
  扰动）；HTTP 层把三类错误分别映射为 409 / 422 / 503。
- **全局锁仅保护对象表与时钟**：判定热路径只取每实例锁，
  全局锁在提交点内极短持有，不构成扩展性瓶颈。

## 5. 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache        # 若默认缓存目录只读

go test ./...                       # 全量
go test -race -count=3 ./...        # 竞态 + 重复
go test ./ontology/ -v              # 逐用例
go test ./ontology/ -bench=. -run='^$' -benchmem   # O(1) 证据
go vet ./... && gofmt -l .

# 手工：服务演示
go run ./cmd/server
# GET  http://localhost:8080/objects/obj-1
# POST http://localhost:8080/submit
#   {"objectId":"obj-1","baseVersion":0,
#    "ops":[{"linkType":"ownedBy","direction":"out","otherId":"u1","add":true}],
#    "maxAttempts":4}
```

测试覆盖索引：

| 规格点 | 测试 |
| --- | --- |
| 基数恰好用尽、依据完整、拒绝即停 | `TestCardinalityExactlyFilled` |
| 恰好空出一个名额，重试基于新读取成功，旧计数不复用 | `TestRetrySeesFreedSlotAndReusesNothing` |
| 重试间基数反复变化，逐次独立读取 | `TestRetryCardinalityOscillates` |
| 预算恰好耗尽（非基数原因），失败尝试不动时钟 | `TestRetryBudgetExactlyExhausted` |
| 单次预算下基线落后即版本冲突 | `TestSingleAttemptStaleIsVersionConflict` |
| 失败尝试外部不可观察 | `TestFailedAttemptsAreInvisible` |
| 多约束分别重校验、任一失败整单拒绝、依据不混同 | `TestMultipleConstraintsAllCheckedSeparately` |
| 唯一名额并发恰好一方成功 | `TestConcurrentSingleSlotExactlyOneWinner` |
| 随机并发 vs 朴素串行模型等价 | `TestRandomConcurrentEquivalentToNaiveSerial` |
| 耗尽情形逐尝试轨迹重放对照 | `TestDeterministicInterleavingReplay` |
| 有界预算、无饥饿 | `TestBoundedRetriesNoStarvation` |
| O(1) 判定开销 | `BenchmarkProjectViolations` |
