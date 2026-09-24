# 一致位点导出器 NOTES

## 第三节推导

初始日志 Seq=1(a)、Seq=2(b)；`Start()` 时 maxSeq=2，故 **S=2**。

| 步 | 动作 | 段 | 发出 Seq | lastEmitted | 已发序列 |
|---|---|---|---|---|---|
| 1 | Write(c)→Seq=3 | — | 无 | 0 | [] |
| 2 | Next | 快照 | 1 | 1 | [1] |
| 3 | Next | 快照 | 2 | 2 | [1,2] |
| 4 | Write(d)→Seq=4 | — | 无 | 2 | [1,2] |
| 5 | Next | 增量 | 3 | 3 | [1,2,3] |
| 6 | Write(e)→Seq=5 | — | 无 | 3 | [1,2,3] |
| 7 | Next | 增量 | 4 | 4 | [1,2,3,4] |
| 8 | Next | 增量 | 5 | 5 | [1,2,3,4,5] |

Finish：快照段 `[1,2]`，增量段 `[3,5]`，已发恰好 1..5 无缝。

- **(甲)** Seq=3 > S=2，属**增量段**。若把 S 错记成 maxSeq+1=3：Seq=3 被误判进快照段，Finish 错报快照 `[1,3]`、增量 `[4,5]`。
- **(乙)** 增量段跳读最新：第 7 步直接发 **Seq=5**（跳过 4）；第 8 步最新仍是 5，要么无条可发、要么**重发 5**。最终已发序列**缺 Seq=4**、可能**重复 Seq=5**，Finish 无缝核验失败。
- **(丙)** Start 推迟到第 1 步后：S=3，快照 `[1,3]`、增量 `[4,5]`。归属必须按 Seq 与 S 比较：到达先后依赖调度时序，同一日志在不同交错下会切出不同的段，破坏确定性；判定分歧点是**第 1 步**——Seq=3 在 Start 之后、首次 Next 之前到达，按 Seq 规则属增量段，按「首次 Next 前到达即快照」的到达规则会错归快照段。

## 四条不变量：保障位置与钉住测试

1. **与朴素全量一致**：`exp.Session.Next` 只发 `lastEmitted+1`（exp/exp.go），绝不跳读；`TestNaiveConsistency`。
2. **段切分正确**：S 在 `api.StartExport` 记录，Next 统一推进，`Finish` 报 `[1,S]`/`[S+1,lastEmitted]`；`TestSegmentSplit`。
3. **全局升序无重复、增量首条=S+1**：Next 单调推进 lastEmitted，每条 Seq 至多发一次；`TestStrictlyIncreasing`。
4. **失败不留痕**：`wlog.Write` 先校验后分配 Seq，exp/api 先查状态再动作；`TestRejectedNoSideEffect`。
