# 链接基数下调的级联处理与孤儿清理设计

本文档说明 `ontology` 包中“运行期基数上限下调”机制的设计取舍、
被放弃的方案与本地验证方法。

## 问题

链接类型的每个方向（`DirectionOut`：按源对象分组；`DirectionIn`：按目标
对象分组）有一个基数上限。上限允许在运行期下调到低于当前已登记数量，
此时必须确定性地处理超额链接，且全过程不产生不一致状态。

## 核心模型

每个 `(链接类型, 方向, 对象)` 构成一个**登记组**（`group`），维护：

- `links`：已登记链接 ID，按排序键 `(Seq, ID)` 升序（`Seq` 为单调登记序号）；
- `pendingList` / `pendingSet`：待处理链接，按标记顺序增量维护；
- `pinned`：经“保留”动作固定的链接，不再参与超额选择；
- `keepOverride`：保留动作提升的有效上限额度；
- `totalEver` / `adjustments`：历史统计计数器。

**有效上限 = 基础上限 + keepOverride**（`Unlimited = -1` 表示不限）。

### 确定性超额选择

目标待处理集合是纯函数：

```
excess = max(0, 已登记数 - 有效上限)
待处理集合 = 已登记且未 pinned 的链接中按 (Seq, ID) 最新的 excess 条
```

同一输入必得同一结果；且该函数只依赖当前有效上限与登记集合，
不依赖历史上限调整轨迹——这直接保证了“多次反复调整等价于从最初
状态跳到最后一次上限的单次调整”（`TestTrajectoryIndependence`）。

### 待处理（pending）状态

超额链接进入待处理而非立即删除：

- 对 `QueryLinks` / `GetLink` 可见，计入 `Stats.TotalRegistered`（历史统计）；
- 不作为有效链接参与基数判断（`CreateLink` 只统计 active 数）；
- 不变量：`pending > 0` 时 `active == 有效上限`。

### 显式决议与默认清理

- `ResolvePending(id, ResolveKeep)`：校验两端对象存在且未撤销，然后链接
  固定为有效（`pinned`）并将该组 `keepOverride +1`（有效上限相应提升）；
- `ResolvePending(id, ResolveDelete)`：最终删除并清理派生状态；
- `FinalizePending(id)`：默认处理路径（孤儿清理）。**对象已撤销的判定
  优先**：任一端对象被撤销则强制删除（`EventPendingForceDeleted`）；
  否则按一般规则——当前有效上限下仍超额的删除，已不超额的恢复；
- `FinalizeAllPending(typeID)`：按 `(Seq, ID)` 顺序批量执行默认路径。

### 上限上调的恢复

上调时按同一纯函数重算目标待处理集合，不再超额的链接按**标记顺序的
逆序**恢复（`pendingList` 为标记顺序栈，从尾部弹出）。上限调回到不低于
登记数量时全部恢复，且因链接从未被物理移除、标记依据在恢复时清除，
恢复后与从未发生过下调再上调完全等价（`TestRestoreOrderReversed`
与一个全新管理器逐链接对比验证）。

### 派生状态

`RegisterDerived` 将派生状态挂到链接上。链接进入待处理时派生状态被
标记 `Stale=true` 并附原因，**内容保留**；`QueryDerived` 原样返回内容但
显式携带 `Stale`/`StaleReason`，调用方不会拿到看似正常但依据已过期的
结果。链接最终删除时派生状态被清理（查询返回 `ErrDerivedNotFound`）；
恢复或保留时恢复可信。链接在任一方向仍待处理时派生状态保持不可信
（`TestDerivedStaleOnlyWhilePending`）。

### 并发线性化

所有变更操作（建链、调限、决议、撤销）在同一把互斥锁下串行生效，
因此并发结果必然等价于按某个全序串行执行。管理器把实际生效的操作
全序记入 `opLog`，测试将该全序重放到一个独立的朴素串行实现
（`naive_test.go`，全量扫描、无增量结构）上逐操作对照接受/拒绝结果，
并比对最终待处理集合（`TestConcurrentDecreaseAndCreate`，`-race` 通过）。
不可能出现“创建请求基于旧上限被接受、但下调认为自己先生效”的矛盾：
锁内先后者先生效，结果唯一。

### 待处理计数的复杂度

`PendingCount` 只读取增量维护的 `pendingList` 长度，为 O(1)，
与历史基数调整次数无关。验证方式：

- `BenchmarkPendingCount`：0 / 1k / 10k 次历史调整后单次查询耗时就绪
  同一量级（实测约 35 / 42 / 48 ns，差异为锁与噪声）；
- `TestPendingCountAfterManyAdjustments`：5000 次调整后增量计数与
  全量扫描结果一致。

### 审计

每个处理动作记录 `AuditEvent{Kind, Trigger, Basis, Disposition, ...}`，
四类关键事件分别暴露：`EventExcessMarked`（下调标记）、
`EventCreateRejected`（上限已满拒绝）、`EventPendingForceDeleted`
（对象撤销强制删除）、`EventPendingRestored`（上调恢复），另有
`EventPendingKept` / `EventPendingDeleted` / `EventLinkCreated` /
`EventLimitChanged` 供完整重放与事后核对。

## 关键取舍

1. **重算而非增量 diff**：每次调限对受影响登记组按纯函数重算目标待处理
   集合，再与现状求差（新进入的标记、退出的恢复）。重算只触及当前登记
   组，成本 O(组内链接数)，换来轨迹无关性的直接保证。
2. **保留 = pinned + keepOverride**：保留动作同时记录“该链接不再参与
   超额选择”与“有效上限 +1”。两者必须成对出现，否则重算会把被保留的
   链接重新标记（它是较新的链接）。
3. **单锁线性化**：正确性优先于吞吐。基数调整是低频管理操作，
   单锁足以支撑，且天然给出可重放的操作全序。
4. **软删除两段式**：先标记待处理、后显式决议，使撤销窗口期内查询可见、
   统计连续、派生状态可回滚。

## 被放弃的方案

- **立即物理删除超额链接**：无法满足“待处理期间可见、计入历史统计、
  可恢复等价”的要求，且派生状态无法回滚。
- **任意/随机选择超额链接**：违反确定性要求，事后无法核对。
- **按对象分组加细粒度锁**：可提升并发度，但跨方向的同一链接需要
  多把锁，引入锁序与死锁风险；收益不足以抵消复杂度。
- **延迟（惰性）计算待处理数量**：查询时扫描历史调整记录重放，
  会使 `PendingCount` 开销随调整次数增长，违反复杂度约束。
- **保留时不提升上限、改为特例标记**：只 `pinned` 不加 `keepOverride`
  会导致有效链接数永久超过有效上限，后续所有基数判断口径混乱。

## 本地验证方法

```bash
go test ./...                 # 全量测试
go test -race ./ontology      # 并发对照（含朴素串行重放）
go test ./ontology -bench PendingCount -benchtime 100000x -run XXX
go run ./cmd/server           # 端到端演示 + 审计轨迹
gofmt -l . && go vet ./...
```
