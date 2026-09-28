# 变更流消费端位点提交器设计

包：`ontology/offset`，核心类型 `offset.Committer`。

## 1. 位点语义的推导

**已提交位点 = 下一条要读的位点**（next-to-read），而不是最后处理完的位点。

推导过程：

1. 崩溃恢复时，消费端需要一个恢复点 `R`，使得"从 `R` 开始重读"既不丢消息也不多重复。
2. 不丢消息 ⟺ 所有未确认的消息都必须被重读 ⟺ `R ≤ min{未确认位点}`。
3. 不多重复 ⟺ 所有已确认的消息都不被重读 ⟺ `R > max{已确认且可不再读的位点}`。
4. 确认是乱序到达的，已确认集合在位点轴上可能有空洞。设 `acked` 为已确认位点集合，则满足 2、3 的唯一起点是：`R = min{ x | x ∉ acked 且 x 已被投递或尚未投递 }`，即从声明起点出发、跳过连续已确认前缀后的第一个位点。
5. 因此不变式为：**小于 `R` 的位点全部已确认，`R` 本身未确认**。`R` 就是"下一条要读的位点"。它天然单调不减（已确认前缀只会变长），且在满足不变式的前提下尽可能大。

由此得到各操作的语义：

- `Declare(key, start)`：声明分区，`R` 初始化为 `start`。
- `Deliver(key, offset)`：逐条登记投递，必须连续（`offset == nextDeliver`），否则无法保证"小于 R 的位点都已确认"的判定只依赖确认集合。
- `Ack(key, offset)`：把 `offset` 加入已确认集合，然后推进 `R` 到连续已确认前缀的末尾。
- 拒绝规则直接来自不变式：
  - `offset < R`：该位点已被提交覆盖，再确认属于**回退**（`ErrOffsetRegression`）；
  - `offset ≥ nextDeliver`：确认了**未投递**的位点（`ErrOffsetOutOfRange`）；
  - `offset ∈ acked`：**重复确认**，幂等成功，状态不变；
  - 空分区键（`ErrEmptyPartitionKey`）、未声明分区（`ErrPartitionNotFound`）、不连续投递（`ErrNonContiguousDelivery`）一律拒绝。

所有拒绝原因都是哨兵错误，调用方用 `errors.Is` 区分。

## 2. 复杂度约束为何成立

要求：推进可提交位点时，扫描次数不得随在途消息数线性增长。

实现：每个分区维护一个**最小堆** `acked`（已确认但尚未被 R 覆盖的位点）和一个同内容的哈希集合 `ackedSet`（O(1) 幂等判定）。推进循环只做一件事：

```
while heap.top == R: pop; R++
```

- 每次 `Ack`：一次堆插入 O(log k)，k 为该分区在途确认数。
- 推进循环的总弹出次数：每个位点从入堆到出堆**整个生命周期至多被弹出一次**，所以任意操作序列的累计弹出次数 ≤ 累计确认次数，与任意时刻的在途规模无关。摊还 O(1) 次弹出/确认。

证明手段（不可外部访问的计数器）：`Committer.scans` 是未导出字段，只在弹出堆顶时递增。测试 `TestScanCountIndependentOfInflight` 以在途峰值 1000、逆序确认（朴素逐位扫描的最坏情况，O(n²)）驱动，断言 `scans == 1000 == 确认总数`——扫描次数只随确认总数增长，与在途规模无关。

## 3. 跨分区共享上限的原子性

- 在途计数：每条 `Deliver` 使全局 `inflight` 加一；每条**首次**确认使全局 `inflight` 减一（重复确认幂等，不重复扣减）。提交位点推进不影响在途计数（确认时已扣）。
- 上限校验：`Deliver` 在生效前先检查 `inflight+1 > limit`，超限则整体拒绝。
- 原子性保证：**全部状态由一把 `sync.Mutex` 保护**，每次操作的"校验 + 生效"在同一个临界区内完成。因此：
  - 不存在"校验通过后被别的 goroutine 抢占额度"的竞态；
  - 拒绝路径在任何状态修改之前返回，被拒绝的操作不改变任何分区的位点、在途计数（测试 `TestSharedInflightLimitAtomicReject` 断言拒绝后 `Inflight()` 与 `Snapshot()` 逐字节不变，且释放额度后被拒的位点仍可原样投递）。
- 一致性快照：`Snapshot()` 在同一临界区内逐字段拷贝所有分区的提交位点，读到的各分区位点来自同一时间点（`TestConcurrentSnapshotConsistent` 在并发读写下断言逐分区单调不减且不越界）。

## 4. 崩溃恢复

提交器本身是内存结构；持久化由调用方负责：定期把 `Snapshot()` 落盘，重启后用 `Restore(limit, snapshot)` 重建——每个分区以其持久化的提交位点作为声明起点。由第 1 节的不变式，重启后消费端恰好重投 `[R, nextDeliver)` 的在途集合：`< R` 的消息不重投（不多重复），`≥ R` 的消息全部重投（不丢）。`TestRestartRedelivery` 完整走了一遍该流程。

## 5. 本地验证方法

```bash
# 全量测试（含竞态检测、输入/提交位点/判定依据日志）
go test -race -v ./offset/

# 关键用例
go test -race -run TestOutOfOrderAck -v ./offset/                # 乱序确认
go test -race -run TestDuplicateAckIdempotent -v ./offset/       # 重复确认幂等
go test -race -run TestAckRegression -v ./offset/                # 位点回退拒绝
go test -race -run TestRestartRedelivery -v ./offset/            # 重启后重投
go test -race -run TestSharedInflightLimitAtomicReject ./offset/ # 共享上限原子拒绝
go test -race -run TestScanCountIndependentOfInflight ./offset/  # 扫描次数与在途无关
go test -race -run TestConcurrent ./offset/                      # 并发一致性
go test -race -run TestDeterministic ./offset/                   # 确定性

# 静态检查
gofmt -l . && go vet ./...
```
