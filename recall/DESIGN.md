# 撤回日志回收器（recall）

按活跃读快照水位回收撤回记录。记录按序号递增追加；快照打开时固定水位，
只可读不超过其水位的记录；回收器回收所有不再被任何活跃快照需要的记录。

## 核心概念

- **序号 `seq`**：撤回记录的全局递增序号，从 `1` 开始，`Append` 单调分配。
- **快照水位 `watermark`**：`OpenSnapshot` 时刻的当前最大序号。该快照可读
  `gcSeq < seq <= watermark` 的记录（`gcSeq` 见下）；打开之后追加的记录
  （`seq > watermark`）对它永远不可见。多个快照可并存。
- **回收位点 `gcSeq`**：已回收上界（含等号）。`seq <= gcSeq` 的记录已被物理
  删除，永久不可再重放。`gcSeq` 单调不减，任何操作都不会让它回退。

## 回收上界

`ReclaimBound`（以及每次 `Reclaim` 实际使用的上界）按下列规则计算：

- 存在活跃快照：`bound = min(所有活跃快照的 watermark)`；
- 无活跃快照：`bound = 当前最大序号`（即全部记录可回收）；
- 初始无任何记录时上界为 `0`。

注意：无活跃快照时上界随追加推进而变大，这不是回退；`gcSeq` 只在 `Reclaim`
时按上界推进，且 `Reclaim` 不会回收超过当前上界的记录，因此即使活跃快照集合
变化，`gcSeq` 仍然单调不减（快照只会关闭、其水位为历史序号，故最小水位
不会因关闭而变小）。

## 回收规则（含等号）

`Reclaim` 删除所有满足 `gcSeq_old < seq <= bound` 的记录，并令
`gcSeq = bound`。边界含等号：序号恰好等于上界（即恰好等于最小快照水位）的
记录也会被回收。因此：

- 仍被某个水位更高的快照需要的记录（`seq > min(watermark)`）不会被回收，
  快照不丢数据；
- 恰好等于最小水位的位点按规格含等号回收——它是最小水位快照的最后一个可见
  位点，该快照重放该位点会得到 `ErrReplayReclaimed`。需要保留含边界位点的
  使用方应持有一个水位更高的快照。

## 错误（互不相同，可用 `errors.Is` 判定）

| 场景 | 错误 |
| --- | --- |
| 重放序号超过快照水位（`seq > watermark`） | `ErrReplayOutOfBound` |
| 重放已回收位点（`seq <= gcSeq`，含非正序号） | `ErrReplayReclaimed` |
| 用不存在/已关闭的快照重放 | `ErrSnapshotClosed` |
| 关闭不存在的快照 | `ErrCloseUnknownSnapshot` |
| 打开快照数超过 `New(maxSnapshots, ...)` 上限 | `ErrTooManySnapshots` |

重放判定顺序为：快照不存在 → 位点已回收 → 超过水位（已回收优先于越界）。
所有拒绝路径在持锁状态内完成检查且不做任何修改，**失败不改变状态**；被拒后
追加、打开、关闭、回收、重放均可继续正常使用。

## 并发

- 内部使用 `sync.RWMutex`：`Append`/`OpenSnapshot`/`CloseSnapshot`/`Reclaim`
  取写锁；`Replay`/`ReclaimBound` 取读锁，读操作彼此并发。
- 快照句柄由内部计数器分配，与序号无关，并发打开不会冲突。
- 并发打开再关闭若干快照后，活跃集合与串行执行完全一致，故 `ReclaimBound`
与 `Reclaim` 的回收上界、回收条数都与顺序执行结果相同（见
`TestConcurrentOpenCloseSameAsSerial`、`TestConcurrentOpenBoundMatchesReference`）。

## 日志

`New(maxSnapshots, logger)` 接受 `*slog.Logger`（传 `nil` 用默认 logger）。
每次操作打印**输入**（序号、快照句柄、水位、上限等）、**结果**（`appended` /
`opened` / `closed` / `replayed` / `rejected`、回收条数与上界）与**判定依据**
（`decision` 字段，如 `watermark pinned to current max seq`、
`delete all seq<=bound inclusively; gc watermark never regresses`、
`state unchanged`）。测试使用内存 handler 捕获并断言日志中包含这些输入、
结果与判定依据。

## 本地验证

```bash
# 全量测试（含竞态检测与详细输出）
go test -race -v ./recall

# 并发用例重复多次，进一步排除偶发
go test -race -count=20 ./recall

# 全工程测试 / 静态检查 / 格式
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖：含等号边界回收、无活跃快照全收、关闭快照后最小水位重估、四类非法
输入拒绝且状态不变、被拒后继续可用、快照上限、以及并发打开/关闭/回收/重放
与串行参照一致、活跃快照区间数据不丢。
