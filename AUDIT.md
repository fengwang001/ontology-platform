# 不变量核对与实测

## 五条不变量

1. **老句柄绝不复活**
   - 保证位置：`table/table.go` 的 `Remove`（释放时递进代号，触顶退役不再复用）与 `lookup`（代号不等即 `ErrStale`，退役即 `ErrSlotExhausted`）；代号不回绕，见 `DESIGN.md` 第 1 节。
   - 钉住测试：`table.TestStaleNeverRevives`（复用 100 次后老句柄仍失效）、`table.TestSlotExhaustion`（退役槽位）、`audit_test.TestConcurrentABA`（并发复用下零次成功）。
2. **句柄不跨表**
   - 保证位置：`handle/handle.go` 的编码含 16 位 tableID；`table/table.go` 的 `tableSeq` 从 1 起逐表分配，`lookup` 先比对 `h.TableID()` 与本表 `id`，不符即 `ErrWrongTable`。
   - 钉住测试：`table.TestDistinctErrors`（跨表句柄在 Get/Remove 上均为 `ErrWrongTable`）。
3. **存活集合自洽**
   - 保证位置：`table/table.go` 的 `alive` 随 Insert/Remove 精确增减；退役槽位状态为 `Exhausted` 且不进空闲链表；`audit/audit.go` 的 `Check` 逐槽核验 `Len+Free+Exhausted==Cap`、空闲链表无重复无在用槽位、存活句柄与槽位代号一致。
   - 钉住测试：`audit_test.TestInvariantEquation`（容量 1/7/64 各 2000 步随机操作后等式成立）、`table.TestSlotExhaustion`（含退役槽位的等式）、`audit_test.TestConcurrentABA` 与 `audit_test.TestConcurrentMixed`（并发结束后等式仍成立）。
4. **失败不留痕**
   - 保证位置：`table/table.go` 的 `lookup` 是纯查询；`Insert` 在 `freelist.Take` 失败时直接返回，不触碰任何槽位；所有校验失败路径在获得锁后只读不写。
   - 钉住测试：`table.TestRejectedOpsLeaveNoTrace`（满表 Insert、零值/跨表/代号不符四类被拒操作前后，`Inspect` 快照逐字段相等）。
5. **零值句柄永不有效**
   - 保证位置：`handle/handle.go` 的 `IsZero`；`table/table.go` 的 `tableSeq` 从 1 起分配使真实句柄 tableID 字段恒非零，`lookup` 第一步即拦截零值；槽位代号从 1 起步使「第 0 代」编码永不合法（`DESIGN.md` 第 2 节）。
   - 钉住测试：`table.TestDistinctErrors`（零值句柄在 Get/Remove 上均为 `ErrZeroHandle`）。

## 取空闲槽位的访问记录数（第四节）

- 机制：`freelist/freelist.go` 的非导出字段 `visited` 记录最近一次 `Take` 访问的槽位记录数；头取尾还，均为 O(1)。
- 钉住测试：`freelist.TestTakeVisitedConstant`（填满后随机释放一半再分配一次，断言上限 4）。
- 实测对照：容量 1000 → 1 条；容量 100000 → 1 条。不随容量增长，靠链表而非扫描。
- demo 对照输出：`访问记录数 1k:1 100k:1 (上限4)`。

## 故障注入对照

- 容量 0/负数 → `ErrBadCap`（`table.TestNewBadCap`）。
- 表满 Insert → `ErrTableFull`，无半插入槽位（`table.TestRejectedOpsLeaveNoTrace`）。
- 代号推到上限（非导出钩子 `table.pushGenToMax`）→ 该槽位退役、末代句柄得 `ErrSlotExhausted`、整表其余可用（`table.TestSlotExhaustion`）。
- 零值 / 跨表 / 已失效 → `ErrZeroHandle` / `ErrWrongTable` / `ErrStale`，互不相同（`table.TestDistinctErrors` 同时断言六个哨兵两两可区分）。
