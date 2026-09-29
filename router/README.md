# 分区保序路由器（router）

`router.Router` 接收「按分区投递、带分区内位点」的变更事件，在接受事件的
同时维护跨分区统计。实现在 `router/router.go`，并发安全。

## 位点连续性规则

- 每个分区的位点从 **0** 开始，必须严格按 `0,1,2,...` 连续递增，无空洞、无重复。
- 分区 `p` 已接受 `count[p]` 条事件后，下一个期望位点就是 `count[p]`；
  出现其它位点即拒绝该次 `Ingest`。
- 同一批 `Ingest` 内，同一分区的事件允许以任意顺序排列——路由器先按位点
  排序再校验，因此**同一批事件的任意交错顺序终态完全一致**。
- 跨分区事件互不约束，可自由交错；某个分区落后只会压住水位，不影响其它分区接受。

## 拒绝原因（原子性）

`Ingest` 是事务式的：批内任一事件非法则**整批拒绝**，各分区缓冲与全部统计
量保持调用前不变（先全部校验、通过后才提交）。错误为 `*RejectError`，用
`errors.Is` 区分：

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrInvalidPartition` | 分区号 `< 0` 或 `>= 分区数` |
| `ErrDiscontinuousOffset` | 位点未从 0 开始、有空洞或重复 |
| `ErrNegativeValue` | 事件值为负 |

`RejectError` 还携带 `Partition` 以及位点不连续时的期望/实际位点（`Want/Got`）。

## 统计量与水位含义

`Stats()` 返回独立拷贝的快照：

- `Partitions[p].Accepted`：分区 `p` 已接受计数（= 下一个期望位点）。
- `Partitions[p].Sum`：分区 `p` 已接受值之和。
- `TotalSum`：所有分区 `Sum` 之和（全局总和）。
- `Watermark`（水位）：**所有分区都已对齐到的最小前缀长度**，即
  `min(count[0], count[1], ...)`。例如三个分区计数为 `5,3,4` 时水位为 3：
  位点 `0..2` 在三个分区都确定存在，可以安全提交；位点 3 起尚有分区缺位，
  不能计入。这也是跨分区乱序不影响最终结果的关键——落后分区只推迟水位，
  不会让任何已对齐的前缀发生回退。
- `CommittedSum`（已提交前缀和）：所有分区中位点严格小于水位的值之和，
  即 `sum_p sum_{off < watermark} value[p][off]`。

上述所有量随喂入单调不减。`Verify()` 依据内部缓冲重算全部统计量做自检，
与 `Stats()` 一样可被并发调用。

## 可复现性：与按位点批量重算对照

流式结果的终态等价于一个与实现无关的参照算法：忽略喂入时序，把全部事件
按 `(partition, offset)` 重新分组排序，然后整体计算各分区计数/和值、
`TotalSum`、`watermark=min(各分区计数)` 及水位以下的前缀和。

本地验证方法：

```bash
# 全量测试（含不同交错顺序、位点不连续/越界/负值拒绝、并发读取、重算对照）
go test -race -v ./router

# 单测日志会打印每条用例的输入、各分区计数与和值、水位、已提交前缀和及判定依据
go test -v -run 'TestInterleavingsConverge|TestConcurrentReadsAndRecompute' ./router
```

对照测试做了两件事：

- `TestInterleavingsConverge`：同一批事件按正序、逆序、多种交错顺序喂入
  独立实例，断言终态（含水位与已提交前缀和）逐字段相同。
- `TestConcurrentReadsAndRecompute`：喂入完成后用 `recompute`（测试内的
  按位点批量重算参照实现）重算，断言与 `Router.Stats()` 逐字段一致；
  喂入期间多个 goroutine 并发 `Stats()`/`Verify()`，并断言终态屏障下并发
  读到的快照逐字段相同。
