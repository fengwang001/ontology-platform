# 不变量审计

## 1. 绝不低估
- 保证位置：`sketch/sketch.go` 的 `Add`（d 格各加 n，格子是落入该格所有键真实计数之和）
  与 `Estimate`（取 d 格最小值，最小值仍 ≥ 真实计数）。
- 钉住测试：`sketch.TestNeverUnderestimate`、`stream.TestEndToEndNeverUnderestimate`。

## 2. 合并同构
- 保证位置：`sketch/sketch.go` 的 `Merge` 逐格相加（加法可交换可结合，与喂入顺序无关），
  开头校验 `w/d` 不一致返回 `ErrIncompatible` 且两草图不动。
- 钉住测试：`sketch.TestMergeIsomorphism`（三个切分点表驱动）、
  `stream.TestMergeCrossParamsLeavesBothIntact`。

## 3. 确定性
- 保证位置：`hashfam/hashfam.go`——种子仅由 `(w,d,row)` 经 splitmix64 混合导出，
  哈希为手写 FNV-1a；无随机源、无时钟、无 map 迭代。
- 钉住测试：`sketch.TestDeterminism`（三档 `(w,d)` 表驱动，两次构建逐格相同）。

## 4. 频繁项不漏
- 保证位置：`topk/topk.go` 的 `HeavyHitters` 用 `Estimate ≥ ⌊φN⌋+1` 判定
  （由不变量 1，真实超阈值 ⟹ 估计超阈值），结果 `sort.Strings` 排序保证顺序确定。
- 钉住测试：`topk.TestNoMissAndDeterministicOrder`（三档 φ 表驱动，零漏报 + 有序）。

## 5. 失败不留痕
- 保证位置：`sketch/sketch.go` 的 `Add`/`Estimate` 先查空键、`Merge` 先查维度；
  `stream/stream.go` 的 `Add`/`Merge` 在触碰任何格子前查容量与参数，
  拒绝路径上没有任何写操作。
- 钉住测试：`sketch.TestRejectedOpsLeaveNoTrace`、`stream.TestCapacityRejectAndRecover`
  （含「超限拒绝后仍可正常使用」）、`stream.TestDistinctDecidableErrors`（五类错误互不相同）。

## 访问格子数实测（第四节）

`sketch.Sketch.accessed` 为非导出字段，经 `atomic.StoreInt64` 记录最近一次
`Add`/`Estimate` 访问的格子数，不出现在任何公开签名中。

| 操作 | w=1000, d=5 | w=100000, d=5 |
|---|---|---|
| 一次 `Add` | 5 | 5 |
| 一次 `Estimate` | 5 | 5 |

- 钉住测试：`sketch.TestAccessCountPerOp`——恰好等于 d，不随宽度变化（不扫整行）。
- `HeavyHitters`：每个候选触发恰好 1 次 `Estimate`（=d 格），与候选数成正比、与 `w×d` 无关；
  两档宽度下调用数同为 1。钉住测试：`topk.TestHeavyHittersAccessProportionalToCandidates`。

## 并发

- 只读路径（`Estimate`/`HeavyHitters`/`SelfCheck`）除原子写 `accessed` 外不写共享状态。
- 钉住测试：`stream.TestConcurrentQueriesIdentical`——16 个 goroutine 经关闭通道同步起跑，
  结果逐位相同；`go test -race` 干净。
