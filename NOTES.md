# NOTES

## 满/空判定方案推导（第三节）

读写指针重合时，「空」与「满」无法区分，必须二选一：

- 方案一（牺牲一个槽位）：以 `(w+1)%cap == r` 判满，最多只能存
  `cap-1` 个元素。于是 `New(4)` 实际只能存 3 个，第 4 次
  `Enqueue` 就会报 `ErrFull`，直接违反第二节第 1 条
  「`New(4)` 能连续 Enqueue 成功 4 次」。
- 方案二（额外维护 `size` 计数器）：`size == 0` 判空、
  `size == cap` 判满，指针重合时由计数器仲裁，最多可存
  `cap` 个元素，满足容量语义。

结论：必须采用方案二（size 计数器）。测试 `TestSacrificeSlotIsWrong`
内联了方案一的错误实现，断言其第 4 次 Enqueue 即报满，反证该方案
不可用；`TestCapacityAndErrors` 钉住正确实现的容量语义。

## 语义与代码/测试对应（第二节）

- 容量语义：`ring.Ring.Enqueue`（ring/ring.go）；`TestCapacityAndErrors`
- FIFO 保序：`ring.Ring.Dequeue`；`TestRandomModelVsRef` 对照 `check.Ref`
- 空/满区分：`Empty`/`Full` 由 size 计数器仲裁；`TestRandomModelVsRef`
  每步断言二者不同时为真、空时 Dequeue 为 (零值,false) 见
  `TestCapacityAndErrors`
- 哨兵错误：`ring.ErrFull`、`ring.ErrBadCap`、`seq.ErrEmpty`，均可被
  `errors.Is` 区分；`TestCapacityAndErrors`
- 资源有界：非导出计数器 `peak`（经 `Peak()` 暴露）；`TestRandomModelVsRef`
  末尾断言 cap=8、10000 次随机操作后 peak ≤ 8
- 并发：`TestConcurrentFIFO` 覆盖 SPSC 与 MPSC，互斥保护，`-race` 干净
