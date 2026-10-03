# TCC 资源预留与协调恢复器 — 设计说明

## 模块
- `ledger`：账户 `bal`/`fz`，`Avail=bal-fz`；Deposit 超 1e15 报 `ErrLimit` 且不改账。
- `tcc`：分支记录 `(xid,br)`，状态 `Tried(acct,amt,deadline)` / `Confirmed` / `Cancelled(reason)`。
- `coord`：协调日志（Begin、成功的 Try、决议）+ `Run/Execute/Recover/Status`。

## 关键取舍
- **取消标记永久保留**：空回滚写入 `Cancelled(Empty)` 后永不回收。容量满时宁可报
  `ErrCapacity` 拒绝空回滚，也不降级成"不留标记"——否则迟到 Try 无法被识别为悬挂。
  放弃方案：标记定时回收（回收窗口内/外都无法同时保证悬挂防护与容量复用）。
- **决议一旦写定不可改**：决议是全局唯一的真相；重复 `Run` 只返回旧决议。
  允许改写会使已 Confirm 的分支与新决议矛盾，破坏可串行化重放。
- **Commit 后遇到期 → 标记人工（Broken/NeedsManual）**：不自动重新 Try。
  重新预留可能因余额已被他人占用而失败，而已 Confirmed 的分支无法回退，
  只能由人工补偿；`Status` 暴露 Broken 集合。
- **到期即取消（`Cancelled(Expired)`）而非独立终态**：到期语义就是"自动释放冻结"，
  与 Cancel 同构，Confirm/再 Try 的拒绝规则可统一（Expired→ErrExpired，Try→ErrHanging）。

## 时钟与到期
- `now ∈ [0,1e15]` 且单调不小于全局时钟；只有成功操作推进时钟，被拒绝不推进。
- 每个带 now 操作先做到期处理：`Tried` 且 `now>=deadline`（恰等即到期）则解冻、记 Expired。
- 被拒绝的操作不落实到期：到期是 `(state,now)` 的纯函数；只读 `Avail` 按虚拟到期返回。
- 最小堆按 deadline 取项：一次操作考察堆项数 ≤ 到期数 + 1（`expiryPeeks` 计数器验证）。

## 并发与不变式
- 单把互斥锁串行化全部状态变更；同分支操作等价于某串行顺序。
- 不变式：Σfz = ΣTried 金额；同分支不会同时 Confirmed/Cancelled；
  Σbal 减少量 = ΣConfirmed 金额。
- coord 崩溃点在"每个分支后"：日志含 Begin 与已成功 Try 列表；`Recover` 无决议写 Abort，
  随后等同 `Execute`（Abort 对未 Try 分支做空回滚，封堵悬挂 Try）。
- 相同操作序列重放结果相同。

## 本地验证
`go build ./... && go vet ./... && go test -race -v ./...`
