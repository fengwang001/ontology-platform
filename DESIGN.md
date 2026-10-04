# 擦除传播与恢复重放账本 — 设计说明

## 结构
- `erase`：账本核心。`Ledger` 持有一把互斥锁与 `Core` 状态，提供
  Request/Ack/Overdue，并通过 `Tx`/`View` 让 hold、restore 在同一锁内
  按统一拒绝次序执行。所有并发操作被串行化，结果等价于某个串行顺序，
  相同操作序列重放得到相同结果。
- `hold`：Hold/Release，做参数与角色校验后调用 `Core`。
- `restore`：Backup/Restore/ReapplyDone/Read，同样只包一层校验。

## 关键取舍（被放弃的方案）
1. 恢复期间读阻塞（ErrRestoring）vs 可读但可能暴露已擦除数据：
   选阻塞。合规上"宁可不可用也不暴露已擦除数据"；可读方案要求调用方
   自行辨别哪些主体已擦除，泄漏风险不可控，放弃。
2. 保留下 Deferred 且时限自解除起算 vs 保留期间照常起算：
   选 Deferred。保留期间依法不得擦除，SLA 无从谈起；照常起算会导致
   解除瞬间即逾期，逾期名单失去意义。Release 时才写 deadline=now+T。
3. 法律保留不撤回已 Active 的擦除 vs 冻结已启动擦除：
   选不撤回。擦除已传播到下游、无法召回；冻结会留下"部分确认"的中间态，
   破坏"Done 必有全部 ack / Deferred 必无 ack"不变式。
4. 重放集在 Restore 时快照（ack(e,s)>tb）vs 动态集合：
   选快照。恢复开始后的新 Ack 不进入待办（例二 e3），语义确定可复现；
   ack 恰等 tb 视为备份已不含该主体数据，不进重放集。
5. Overdue 效率：Active 擦除单按 (deadline,id) 存于有序切片，Overdue
   从头扫描、遇首个未逾期即停，检视数 ≤ 返回数+1，与 Active 总数及
   已 Done 数量无关（非导出计数器，1000/100000 两档断言）。

## 不变式
- Done ⇒ 每个系统均有 ack；Deferred ⇒ 无任何 ack；Restoring ⇒ 待办非空。
- 被拒操作不改变擦除单、保留标志、备份点、系统状态、编号计数与最大 now。
- 拒绝次序：参数越界 → 权限 → 时钟回退 → 状态类；Restore 内先
  ErrNoBackup 再 ErrState；Overdue 只读不验时钟，Read 只报 ErrRestoring。

## 本地验证
- `go test ./...`：单元、边界、计数器两档、2000 组随机序列对拍
  （`-v` 打印每组输入、输出与判定依据）。
- `go test -race ./...`：并发等价于串行。
- `gofmt -l . && go vet ./...`。
