# 两阶段分布式屏障 — 推导与不变量落点

## 第三节：N=3，P1/P2/P3 七步分步表

| 步 | 操作 | 已到达集 | 已离开集 | 轮次 | Released | 结果 |
|---|---|---|---|---|---|---|
| 1 | Arrive(P1) | {P1} | {} | 0 | 否 | 成功 |
| 2 | Arrive(P2) | {P1,P2} | {} | 0 | 否 | 成功 |
| 3 | Arrive(P3) | {P1,P2,P3} | {} | 0 | 是 | 成功：到达集满 N，释放，转离开阶段 |
| 4 | Depart(P1) | {P2,P3} | {P1} | 0 | 是 | 成功 |
| 5 | Arrive(P1) | {P2,P3} | {P1} | 0 | 是 | 失败：P1 已离开本轮，轮次未结束，拒绝且状态不变 |
| 6 | Depart(P2) | {P3} | {P1,P2} | 0 | 是 | 成功 |
| 7 | Depart(P3) | {} | {} | 1 | 否 | 成功：离开集满 N，本轮结束，清空两集、轮次+1 |

**(甲)** 第 5 步正确结果是拒绝（ErrArriveAfterDepart）：P1 已离开本轮，须等 P2、P3 都离开才能进下一轮。若用单计数器（Arrive 计数到 N 即清零、不分阶段），P3 到达后计数已清零，P1 这次 Arrive 会被记成「下一轮第 1 次到达」——快进程的下一轮到达与 P2/P3 尚未完成的本轮离开混在同一计数里，提前凑数、提前释放。

**(乙)** 未到齐时 Depart（如第 2 步后 P2 就 Depart）正确处理是拒绝（到达阶段内 Depart 非法）。若允许，已到达集在凑满 N 前就被缩减，「到齐才释放」的判定被架空——违反第二节第 1 条不变量。

**(丙)** 单相门闩在 P3 到达时立即清零，第 5 步 P1 的 Arrive 会被当作「新一轮」的首次到达而接受；此时 P2、P3 仍处在本轮离开阶段，新旧两轮状态交叠：新一轮计数里混着上一轮的离开，轮次隔离（第 3 条不变量）被破坏。

## 第二节四条不变量的代码落点与钉住它们的测试

1. **到齐才释放**：`phase/phase.go` 中 `Round.AddArrival` 仅在已到达集满 n 时把阶段置为 Departing，`Released` 只读该阶段位 → `TestReleaseOnlyWhenFull`
2. **离开才推进**：`bar/bar.go` 中 `Barrier.Depart` 仅当 `MoveToDeparted` 报告离开集满 n 才 round+1 并重置单轮状态 → `TestAdvanceOnlyWhenAllDepart`
3. **轮次隔离**：`bar/bar.go` 中 `Barrier.Arrive` 在任何状态修改前先查 `HasDeparted`，命中即返回 `ErrArriveAfterDepart` → `TestRoundIsolation`
4. **失败不留痕**：`bar/bar.go` 中 `Arrive`/`Depart` 的全部校验都先于任何写操作，校验失败直接返回 → `TestRejectionLeavesNoTrace`
