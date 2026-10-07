# 链接基数下调的级联处理与孤儿清理设计

## 1. 问题与范围

链接类型某方向的基数上限可在运行期被下调。下调后，已经登记、数量超出
新上限的既有链接不能被静默删除，也不能任意挑选“牺牲者”，否则会产生
不可复现、不可审计的不一致状态。本设计给出一套确定性的、可并发安全
串行化的处理机制，覆盖：超额标记、待处理生命周期、显式处置（删除/
保留并提升有效上限）、上调整体恢复、派生状态级联、对象撤销孤儿清理、
并发全序等价性，以及与历史调整次数无关的计数开销。

代码位于 `cardinality` 包，入口为 `Engine`；无外部依赖。

## 2. 核心模型

- **基数桶 `BucketKey{LinkType, Direction, SourceID}`**：基数约束的最小
  作用域。一个源对象在某链接类型的某方向上持有一个桶。
- **链接三态**：
  - `active`：有效，参与所有依赖基数的判断；
  - `pending`：待处理。对查询仍可见、仍计入 `RegisteredCount` 历史统计，
    但不占用有效容量、不参与基数判断；
  - `deleted`：最终物理删除，从桶成员集合移除。
- **确定性全序**：桶内所有已登记未删除链接按 `(登记序号 Seq 升序,
  LinkID 字典序)` 排序。`Seq` 是引擎全局单调递增序号，创建即固定，
  不受后续任何调整影响。
- **有效容量 effectiveCapacity**：
  `max(基础上限 baseLimit, 全序中最后一条“保留”链接的名次)`。
  “保留”链接是经显式处置确认保留的链接，保留即等价于把该方向有效
  上限提升到必须容纳它，且会联动恢复排在它前面的待处理链接。

## 3. 关键机制

### 3.1 下调：确定性超额标记

每次 `SetLimit` 都做一次**全量对账 reconcile**（幂等、无历史依赖）：

1. 取桶内全序链接；
2. 计算当前有效容量；
3. 名次超出容量且当前为 `active` 的链接，按全序升序逐条标记为
   `pending`，同一次对账共享一个标记批次（`MarkedAtSeq`）；
4. 每条链接保存 `MarkBasis`：`{标记时上限, 名次, 总数, 完整有序 ID 列表}`。

选择规则因此完全确定：同一组输入重复执行，超额集合、批次顺序与
`MarkBasis` 逐字节相同（`TestDeterministicSelection`）。

被放弃的方案：
- *按最后更新时间淘汰（LRU）*：更新时间可相同、可被客户端伪造，且
  时钟回拨会破坏确定性；
- *随机/哈希抽样*：不可重复、无法审计；
- *由调用方指定牺牲者*：把平台一致性责任外推，且无法证明串行等价。

### 3.2 待处理与显式处置

标记后链接进入 `pending`，不会被立即物理删除。处置必须由显式动作
`Finalize(linkID, disposition)` 触发：

- **删除**：置 `deleted`、移出桶、清理派生状态，随后对账让更靠后的
  链接按同一确定性规则补位；
- **保留**：动作执行时**重新校验**目标对象当前仍有效（调用
  `objectValidFn` 及内部撤销表）。校验通过才把链接加入保留集合，
  有效容量随之扩大，再由对账恢复其自身及更早的待处理链接；
  校验不通过则落入下面的强制删除路径。

### 3.3 对象撤销：孤儿清理优先路径

`RevokeObject` 记录对象撤销。判定优先级如下（`Finalize` 内严格按序）：

1. **若链接目标对象已撤销 → 无论请求意图为何，只能删除**，原因码
   `pending_target_revoked_force_delete`；
2. 否则才按请求的保留/删除默认路径处理。

同时，对账时目标已撤销的 `pending` 链接**不允许被上调自动恢复**，
避免出现“恢复成功但对象已是孤儿”的状态。这样处理结果确定且无遗留
不一致。

### 3.4 上调：逆序整体恢复

对账中，名次重新落入容量内、且目标仍有效的 `pending` 链接恢复为
`active`。恢复顺序严格为标记顺序的逆序：先按标记批次
`MarkedAtSeq` 降序（最近标记的先恢复），同批内按名次降序。恢复时
清除 `MarkBasis` 并恢复其派生状态。

当上限调到不低于当前已登记数量时，所有（未撤销的）待处理链接全部
恢复，最终 active 集合、容量与“从未发生过下调”的参照引擎逐项相等
（`TestAlternatingDowngradeUpgrade`）。

### 3.5 轨迹无关性（跳转等价）

因为每次 `SetLimit` 都是**只依赖当前状态**的幂等全量对账，不保存
“调整轨迹”，所以：

- `3→1→5→2→4` 的反复震荡与直接 `4`，最终 active/pending/容量一致；
- 对同一条 pending 链接，在两种轨迹上做相同处置，去向一致。

最终去向只由“进入待处理时的标记依据（名次不变）”与“结束前最后一次
生效的容量”共同决定。测试：`TestTrajectoryIndependence`。

### 3.6 派生状态级联

派生状态（`DerivedState`）依赖某条链接的存在性：

| 链接变化 | 派生状态动作 |
| --- | --- |
| 进入 pending | 标记 `Suspect=true`，**数据保留不清空** |
| pending→active（恢复/保留） | 解除 Suspect，恢复可信 |
| 最终删除 | `Cleared=true`、清空 Payload |

查询 `QueryDerived` 在 Suspend/Cleared 期间返回 `Fresh=false` 且带
`Notice` 明确告知依据已过期，绝不返回看似正常的结果。`PendingLinks`
视图额外列出受牵连的派生状态 ID。

### 3.7 并发：全序串行化

`Engine` 用单一互斥锁串行所有变更操作，每个操作在锁内取全局 `Seq`。
因此下调与创建的任何实际交织，都等价于按这些 `Seq` 排成的某条全序
串行执行；创建请求在其全序位置读取的一定是当时已生效的容量，不存在
“按旧上限接受、下调却声称先生效”的矛盾。

`TestConcurrentInterleavingMatchesSerialTotalOrder` 用 8 个 goroutine
 并发交织下调与创建，随后：

1. 按实际 `OrderSeq` 排序操作；
2. 用**独立编写的朴素串行实现** `naiveEngine` 从同一初始状态重放；
3. 逐条比对接受/拒绝、每步标记/恢复集合，并比对终态 active、pending、
   登记数、容量。

### 3.8 计数复杂度

桶内维护增量字段 `pendingCount`，标记 +1、恢复/删除 -1。
`PendingCount` 为一次字段读取，**开销为 O(1)，只与当前待处理集合
相关，与历史调整次数无关**（即便实际枚举 `PendingLinks` 也只扫描
当前成员）。

可验证方式：
- `TestPendingCountIndependentOfHistory`：200 次震荡后计数仍精确等于
  当前待处理集合大小；
- `BenchmarkPendingCount`：2 次调整与 2000 调整后的单次查询均约
  45ns/op，无随历史增长的趋势（可用 benchstat 对照）。

## 4. 四类情形的独立原因码

| 情形 | `Reason` |
| --- | --- |
| 下调触发标记为超额 | `marked_excess_by_limit_downgrade` |
| 新建因有效上限已满被拒 | `create_rejected_limit_full` |
| 对象撤销只能删除（优先） | `pending_target_revoked_force_delete` |
| 上调导致整体恢复 | `pending_restored_by_limit_upgrade` |

此外保留/删除/派生状态三种变化也有独立原因码。所有处理都写审计记录
（`AuditRecord`）：含全序序号、桶、链接、触发原因、`MarkBasis` 快照
与最终去向，可通过 `AuditLog()` 做事后核对。

## 5. 关键取舍与边界

- **全量对账而非增量打补丁**：换取幂等、轨迹无关、可证明正确；
  代价是 SetLimit 为 O(n log n)（n 为桶内当前登记数）。基数桶规模
  通常很小，选择正确性优先。
- **“保留”建模为提升有效容量而不是豁免单链接**：避免保留集合与
  容量计算长期打架；提升后更早的 pending 链接自然按规则联动恢复，
  语义完整。
- **方向建模**：当前实现以 `Outgoing` 桶承载 `CreateLink`；
  `Incoming` 可由上层按对端对象对称登记，核心机制对方向参数化，
  不改变任何规则。
- **删除有效链接不在本机制范围**：有效链接的主动删除属于上层 API；
  本机制负责的是“被上限挤入待处理”的链接生命周期。
- **撤销的作用域**：撤销影响 pending 链接的恢复资格与保留校验；
  已 active 链接不被本机制级联强删，避免越权变更上层事实。

## 6. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./...
go test -race -v ./cardinality
go test -bench=BenchmarkPendingCount -benchmem ./cardinality
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
gofmt -l . && go vet ./...
```

测试矩阵：

- `TestAlternatingDowngradeUpgrade`：连续下调/上调交替后的集合与逆序恢复；
- `TestDeterministicSelection`：重复执行的超额集合与依据一致；
- `TestCreateRejectedByNewLimit`：按当前有效上限拒绝与删除后补位；
- `TestRetainRaisesEffectiveLimit`：保留提升容量并联动恢复；
- `TestRevokedObjectForcesDelete` / `TestFinalizeRechecksCondition`：
  撤销优先与保留前重新校验；
- `TestDerivedStateSuspensionAndVisibility`：派生状态可见性与过期标注；
- `TestConcurrentInterleavingMatchesSerialTotalOrder`：并发交织对拍朴素串行实现；
- `TestPendingCountIndependentOfHistory` / `BenchmarkPendingCount`：
  计数正确性与复杂度。
