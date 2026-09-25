# 物化视图原子切换读一致 — 推导与不变量

## 八步推导（初始 cur = {Seq:0, Cells:{}}）

| 步 | 操作 | cur (Seq, Cells) | staging | Read 返回 |
|---|---|---|---|---|
| 1 | Read() | {0, {}} | 无 | (0, {}) |
| 2 | StartRefresh() | {0, {}} | {}（Seq0 的拷贝） | 无 |
| 3 | Stage(a,1) | {0, {}} | {a:1} | 无 |
| 4 | Stage(b,x) | {0, {}} | {a:1, b:x} | 无 |
| 5 | Read() | {0, {}} | {a:1, b:x} | (0, {}) |
| 6 | Commit() | {1, {a:1, b:x}} | 无 | 无 |
| 7 | Read() | {1, {a:1, b:x}} | 无 | (1, {a:1, b:x}) |
| 8 | StartRefresh; Stage(b,y); Abort | {1, {a:1, b:x}} | 无 | 无 |

- **(甲)** 正确实现：读者已 pin 住旧快照对象，读 Seq、读 Cells 都落在同一个不可变对象上，仍得 **(0, {})**，绝不混杂。若 Commit 就地修改 cur 指向的对象：读者「读 Seq」落在 `cur.Seq=1` 之后、「读 Cells」落在 Cells 被改之前，会读到 **(1, {})**——新 Seq 配旧 Cells 的混杂错值（也可能读到 (0, {a:1,b:x})，同样是混杂）。
- **(乙)** Abort 后 Read 应得 **(1, {a:1, b:x})**。若 StartRefresh 直接返回 cur 的引用（不拷贝），`Stage("b","y")` 就地污染 cur，Abort 丢弃不了任何东西，Read 错成 **(1, {a:1, b:y})**；更早地，第 3 步 `Stage("a","1")` 后立刻 Read 会错成 **(0, {a:1})**——暂存写提前泄漏。
- **(丙)** 正确实现：Commit 后到达的 `Stage("c","2")` 落入**新一代暂存**（下一次 Commit 后成为 Seq=2 的内容），cur 仍为 V1，Read 看不到 c。若 Stage 直接写 `cur.Cells`（无暂存），V1 被就地污染，并发 Read 会错读 **(1, {a:1, b:x, c:2})**——seq 仍是 1 却多出了未提交的 c。

## 四条不变量：代码位置 → 钉住它的测试

1. **提交日志重放一致**：`view.Commit` 把暂存固化为 `Seq=cur.Seq+1` 的新不可变快照（view.go `Commit`）→ `TestCommitLogReplay`
2. **读一致性**：`Read` 只做一次 `atomic.Load`，之后只读该快照对象（view.go `Read`）→ `TestReadConsistencyPinned`
3. **原子切换+回退**：`Stage` 只写暂存、`Commit` 原子换指针且 Seq 恰 +1、`Abort` 丢暂存不动 cur（view.go）→ `TestAtomicSwitchRollback`
4. **失败不留痕**：三类哨兵错误在任何状态修改之前返回（view.go 各方法开头）→ `TestFailureNoTrace`
