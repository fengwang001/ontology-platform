# consistentread：带滞后上限的一致性读取器

`consistentread.Reader` 维护两条单调推进的位点，提供「最多滞后 `maxLag`
个已提交偏移」的快照读取，并支持降级与阻塞两种模式。

## 位点模型

- `Position{Epoch, Index}`：`Index` 是连续日志偏移；`Epoch` 是该偏移提交时
  的纪元（例如主从任期），允许随提交向前跳跃，但不得倒退。
- 提交位点 `committed`：由 `AdvanceCommit` 推进。首个位点必须是
  `Index=0`，之后每次必须恰好前进 1（不允许跳号或倒退）。
- 已应用位点 `applied`：由 `AdvanceApply` 推进。必须按 `Index` 从 0 开始
  逐个推进，不能越界（超过 `committed`），且 `Epoch` 必须与提交时记录的
  纪元逐字段一致，即应用的位点必须确实被提交过。
- 不变量：`applied` 恒不超过 `committed`，可用并发安全的 `Check()` 自检。
- 滞后定义为连续偏移个数：`lag = committed.Index - applied.Index`。

被整体拒绝的操作（失败原因可通过 `*Failure` 的 `Reason` 字段区分）：

| 场景 | Reason |
| --- | --- |
| `maxLag < 0` | `ReasonNegativeLag` |
| 提交倒退或不严格推进 | `ReasonCommitNotAdvanced` |
| 提交跳号（不连续）、首个提交非 0 | `ReasonCommitNotContiguous` |
| 应用位点超过提交位点 | `ReasonApplyAheadOfCommit` |
| 应用位点乱序/重复 | `ReasonApplyOutOfOrder` |
| 应用位点从未以相同纪元提交 | `ReasonUnknownPosition` |

任何失败都不会修改两条位点，也不会唤醒或改变等待者状态（校验全部在
状态变更之前完成）。

## 两种读取语义

`Read(mode, maxLag)` 返回的 `Snapshot` 始终对应一个已经被应用（因此可复现）
的确定位点。

### `ModeDegrade`（降级）

- 立即返回当前 `applied`，绝不等待。
- 当 `lag > maxLag` 时：返回 `applied`，且 `Degraded=true`。
- 当 `lag <= maxLag` 时：返回 `applied`，`Degraded=false`。
- 边界：`lag == maxLag` 算满足约束（不降级）；`maxLag=0` 且
  `applied==committed` 时即为强一致读取。

### `ModeBlock`（阻塞）

- 在进入调用的瞬间（持锁读取）冻结目标 `target = committed`。
- 若 `applied` 未达到 `target`，则阻塞等待，直到 `AdvanceApply`（或
  `AdvanceCommit`）推进并通过条件变量广播唤醒；被唤醒后只复核自己冻结的
  `target`，不重新读取最新提交位点。
- 因此阻塞期间 `committed` 继续推进不会移动本次结果：返回位点恒等于
  `target`，且 `Degraded` 恒为 `false`。一次广播可并发放行多个等待者。

## 并发与可复现性

- 内部使用互斥锁 + `sync.Cond`；`Positions`、`Check`、`Read` 均可并发
  调用。
- `Positions()` 在同一把锁内读出两个位点，保证调用方看到的
  `committed/applied` 是逐字段一致的成对快照。
- 阻塞等待通过 `Broadcast` 唤醒，多个等待者被同时放行并各自复核自己的
  冻结目标，无惊群错放问题。

## 本地验证

测试（`reader_test.go`）内建一个与实现无关的朴素参照 `naiveReader`：
它只用普通整数记录 `committed/applied`，以最直白的规则推导降级判定
（`committed-applied > maxLag` 即降级）和阻塞结果（返回调用时刻冻结的
目标）。每个用例都把实现结果与朴素参照逐字段核对。

```bash
# 全量测试（含竞态检测）；-v 可看到每个用例打印的
# 操作、committed、applied、返回位点、degraded 与判定依据
go test -race -v ./consistentread

# 重复多轮，放大调度不确定性
go test -race -count=10 ./consistentread

# 覆盖率与静态检查
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
go vet ./...
gofmt -l .
```

覆盖场景：降级判定边界（`lag` 分别在 `maxLag` 的两侧及恰好相等）、
`maxLag=0` 的强一致与滞后降级、阻塞被应用推进唤醒、提交继续推进不改变
冻结目标、一次广播放行多个等待者、七类可区分拒绝且失败后位点不变、
并发查询/自检/读取的逐字段一致性。
