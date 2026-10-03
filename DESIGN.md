# TCC 资源预留与协调恢复器 — 设计说明

## 分层
- `ledger`：账户余额 `bal`、冻结额 `fz`，可用额 `bal-fz`；纯账目，无时钟。
- `tcc`：分支记录 `(xid,br)`，状态 Tried/Confirmed/Cancelled；内部持有一个 ledger 与到期最小堆、全局时钟。
- `coord`：全局决议与恢复；只依赖协调日志（Begin、成功的 Try、决议），崩溃后从日志重建状态。

## 关键取舍
- **取消标记永久保留**：Cancelled(Empty/Cancel/Expired) 不回收。容量满时空回滚宁可返回 ErrCapacity，也不留标记——
  否则迟到的 Try 会成功，悬挂防护失效。放弃的方案是“标记定时回收”：回收窗口内迟到 Try 无法被稳定识别，
  且回收本身引入额外时钟竞争与非确定性。
- **决议一旦写定不可改**：Commit/Abort 是恢复时唯一权威输入；可改决议会让崩溃后无法判断 Run 是否完成，
  破坏“相同序列重放结果相同”。
- **Commit 后到期标记人工（Broken/NeedsManual），不自动重新 Try**：已 Confirm 的分支无法回退，
  重新预留可能因余额已被他人占用而失败，产生不可闭合的中间态；人工处理是唯一安全选择。
- **到期视为取消（Cancelled(Expired)）而非独立终态**：到期在账目上就是释放冻结，语义等同取消；
  独立终态会让 Cancel/Confirm 多出无法幂等的分支，且堆只需一条扫描路径。

## 到期与时钟
- 最小堆按到期时刻索引化维护；每次操作只弹出/查看“到期数+1”个堆项，计数器按次操作重置。
- 所有带 now 操作先做（或只读操作虚拟）到期处理，再做状态判定；参数/时钟非法时不落实任何变化。
- now 非法或 now<全局时钟即 ErrClock；只有成功推进时钟；拒绝为 now 的纯函数，可重放。

## 错误优先级
参数非法 → ErrClock → 状态类（Hanging/Mismatch/State/NoBranch/Expired/Conflict）
→ 资源类（Capacity 先于 Insufficient；ledger 的 ErrLimit 属资源类）。

## 并发与崩溃
- tcc 一把互斥串行化分支状态与账目，堆、记录、账户在同一临界区变更，天然满足 fz 合计=Tried 合计。
- coord 日志追加与 Try 效果在同一原子步骤（注入式崩溃点模拟追加后崩溃）；
  Recover 无决议写 Abort，随后按决议 Confirm/Cancel（Abort 对每个分支 Cancel，未到达分支即空回滚）。

## 本地验证
`go build ./... && go vet ./... && go test -race -v ./...`
