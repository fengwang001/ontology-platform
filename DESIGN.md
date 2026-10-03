# 工作流更新处理器设计说明

## 结构

- `history`：单实例追加式事件日志。事件为 `U(uid, delta)`、`A(uSeq)`、`C`，序号从 1 连续；`C` 至多一条且必为末条。日志对象由 handler 的存储层持有，崩溃后仍在，Recover 只读它。
- `dedupe`：泛型 FIFO 去重表，容量 K（1..1e6）。`Add` 在满时淘汰最早登记者，命中不刷新顺序。handler 存 `*Result` 指针，结果的就地变更（Accepted→Completed/Aborted）不触碰表结构。
- `handler`：实例状态机。每实例一把锁，不同实例可并发；同实例并发等价于某个串行序，同 uid 并发由"先查表、后登记"在锁内完成，只校验一次。

## 关键取舍

1. **被拒绝的更新不入历史**。历史只记录已接受的事实（U/A/C），日志更小、重放更快；代价是崩溃后 Rejected 的去重登记丢失，同 uid 重试会重新校验、可能被接受。被放弃的方案：把 Rejected 也写历史——能精确复现拒绝，但日志会被恶意或重复的非法更新撑大，且拒绝本就不改变状态，记日志收益低。
2. **校验用投影值而非已应用值**。投影值 = 已应用值 + 已接受未应用的 delta 之和。若只看已应用值，排队的更新合起来会越界（如 cap=10 时 +6、+6 都"通过"但合计 12）。投影值用增量计数器维护，校验不遍历队列，队列长度 1 与 10000 的校验成本相同。
3. **去重表有界（FIFO 淘汰）而非无限**。无限表在长运行实例上内存无界；有界 FIFO 保证内存不超过 K，代价是被淘汰的 uid 重试会被当作新请求重新校验，这与"崩溃丢失 Rejected"的语义一致：去重只是优化，正确性由历史保证。
4. **cap 与 K 是配置不是状态**。Create 时记入配置表，Recover 不重建它们，只从日志重建 s、队列、结果与去重表。Recover 对任意历史前缀成立：A 事件数 m 即前 m 个 U 已应用（队列严格 FIFO），其余 U 为待应用；C 之后未被 A 覆盖的 U 推出 Aborted。

## 错误优先级

参数非法 > ErrNotFound/ErrExists > 去重命中（原样返回）> ErrClosed > ErrEmpty。参数错误、ErrClosed、ErrEmpty 不写历史、不登记去重表。

## 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -v ./history/... ./dedupe/... ./handler/...
go vet ./... && gofmt -l .
```

测试覆盖：题目示例、投影 vs 已应用校验、去重命中先于 ErrClosed、Rejected 不耗序号、FIFO 淘汰、逐前缀 Recover、随机操作序列对拍朴素模拟、-race 并发。
