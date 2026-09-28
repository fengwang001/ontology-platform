# 变更流消费端位点提交器设计

包：`ontology/committer`。为多个分区接收乱序到达的确认位点，在跨分区共享的在途上限约束下，原子地推进各分区可安全提交的位点。

## 1. 位点语义的推导

**已提交位点 = 下一条要读的位点**，而非最后处理完的位点。由此推导：

- 设已提交位点为 `C`，则"小于 `C` 的消息都已确认"必须恒成立。崩溃重启后从 `C` 重新投递：
  - **不丢**：任何未确认的消息位点必然 `>= C`（否则它已确认，矛盾），必然被重新投递；
  - **不多**：任何 `< C` 的消息已确认，不会被重新投递。
- `C` 必须**尽可能大**：每多推进一位，重启后就少重复一条。因此每次确认后都要把 `C` 推进到连续已确认前缀的尽头。
- `C` 必须**单调不减**：重启后从 `C` 续读，若 `C` 回退，已确认消息会被重复投递（违反"不多"）；且小于 `C` 的消息可能已被上层当作"已完成"清理。故确认位点 `< C` 属于回退，必须拒绝（`ErrAckRegression`），而不是静默忽略——它意味着调用方持有的位点视图已过期，是编程错误。
- **越界确认**（确认了未投递的位点，`>= nextDeliver`）同样必须拒绝（`ErrAckOutOfRange`）：确认一个从未投递的位点意味着位点空间被污染，若接受则可能把未投递的消息错误地圈进"已确认"区间，重启后丢消息。
- **重复确认幂等**：位点在 `[C, nextDeliver)` 内且已确认过，返回成功且不改任何状态。网络重试/重启重投场景下重复确认是常态，不应视为错误。

### 状态不变量

每个分区维护：`committed`（C）、`nextDeliver`（D）、`inFlight`、`pending`（已确认但 C 尚未越过的位点集合）。不变量：

1. `C <= D`，且 `C` 单调不减；
2. `[C, D)` 内的位点要么在 `pending` 中（已确认），要么在途（未确认）；
3. `inFlight = (D - C) - |pending|`；
4. `pending` 中不存在 `< C` 的位点（推进时即时清除）。

投递要求从 `D` 开始连续递增（`ErrNonContiguousDelivery`），保证位点空间无洞，使"小于 C 都已确认"的判定只依赖 `pending` 成员关系。

## 2. 复杂度约束为何成立

要求：推进可提交位点时不得随在途消息数线性增长地反复扫描。

实现：确认到位时，若该位点恰为 `C` 或填补了 `C` 处的空洞，则循环 `C++`，每步从 `pending` 删除一个条目（`O(1)` 哈希删除）。关键观察：

- 每一次循环迭代都**消耗一个 `pending` 条目并推进 `C` 一位**；
- 每个位点一生中至多进入 `pending` 一次、被删除一次；
- 因此全生命周期**总迭代次数 == C 的累计推进量 == 已确认位点总数**，摊还 `O(1)`/次确认，与任一时刻的在途规模无关。

对比朴素实现"每次确认都从头扫描在途集合找连续前缀"：在途 `n` 时每确认一次扫 `O(n)`，总代价 `O(n²)`。本实现不存在这种反复扫描。

**证明方式**：内部计数器 `advanceScans`（小写、不导出，外部无法访问）记录推进循环的总迭代次数。测试 `TestAdvanceScansIndependentOfInFlight` 在在途峰值 9999 的场景下断言 `advanceScans == 10000 == 总推进量`——若为 `O(n²)` 扫描，该值会是约 10⁸ 量级。

## 3. 跨分区共享上限的原子性

- 全局维护 `inFlight <= maxInFlight`，所有分区共享。
- `Deliver` 的扣减是**先校验、后扣减**，且整个校验+扣减在同一把互斥锁 `mu` 的临界区内完成：
  1. 锁内校验连续性（`offsets[i] == nextDeliver + i`）；
  2. 锁内校验 `inFlight + len(offsets) <= maxInFlight`；
  3. 任一失败直接返回，**不写任何字段**——位点、在途计数、已产生结果均不变（测试 `TestSharedLimitAtomicRejection` 用拒绝前后快照 `DeepEqual` 证明）；
  4. 全部通过才一次性更新 `nextDeliver`、`inFlight`（分区与全局）。
- 因为所有读写都在 `mu`（读操作用 `RWMutex` 的读锁）保护下，不存在"两个分区并发投递同时通过校验、合力超限"的竞态——校验与扣减对彼此原子。
- `Ack` 在锁内同时扣减分区与全局在途计数，释放额度立即可被其他分区的 `Deliver` 使用。

## 4. 一致性、并发与确定性

- **并发只读逐字段一致**：`Snapshot`/`SnapshotAll` 在单次读锁内一次性拷贝全部字段，调用方拿到的快照满足不变量（`C <= D`、`inFlight >= 0`、`inFlight <= D - C`），不会出现撕裂读。测试 `TestConcurrentReadsConsistent` 以 3 写 8 读并发压测，`-race` 下校验不变量。
- **并发确认与朴素逐位检查一致**：`TestConcurrentAcksMatchNaive` 对 8 个分区各 500 条做随机置换并发确认，最终结果与"全部确认 ⇒ C == 投递终点、在途 == 0"的朴素结论一致。
- **确定性**：`TestDeterministic` 用同一输入序列重复计算 20 次，快照 `DeepEqual` 完全相同。实现不依赖 map 迭代顺序影响结果（结果按分区键索引，比较按内容）。

## 5. 本地验证方法

```bash
# 全量测试（含竞态检测与日志中的输入/提交位点/判定依据）
go test -race -v ./committer/

# 代码检查
gofmt -l .
go vet ./...
```

测试覆盖矩阵：

| 用例 | 测试 |
|---|---|
| 乱序确认 | `TestOutOfOrderAck` |
| 重复确认幂等 | `TestDuplicateAckIdempotent` |
| 位点回退拒绝 | `TestAckRegression` |
| 重启后重新投递集合 | `TestRestartRedelivery` |
| 共享上限占满原子拒绝 | `TestSharedLimitAtomicRejection` |
| 非法输入（空键/未声明/不连续/越界/回退） | `TestInvalidInputs` |
| 扫描次数与在途规模无关 | `TestAdvanceScansIndependentOfInFlight` |
| 并发只读一致 | `TestConcurrentReadsConsistent` |
| 并发确认 == 朴素逐位检查 | `TestConcurrentAcksMatchNaive` |
| 确定性 | `TestDeterministic` |
