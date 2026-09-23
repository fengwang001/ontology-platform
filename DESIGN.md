# 分片扇出查询的部分失败合并器 — 设计推导

模块 `ontology`，仅用标准库，状态在进程内存，分片以接口注入（无真实网络）。
包：`shard`（接口/假分片）、`fanout`（并发+截止时间）、`combine`（五种聚合）、
`confidence`（可信度与区间）、`report`（报告）、`cmd/demo`。

## 1. 数据模型与分片契约

- 记录 `Record{ID string, N *int64, Score *int64}`：`N` 为计数/求和度量
  （Count 每条记 1，Sum 记 N），`Score` 为 TopK 排序键；指针 nil 表示该字段缺失。
- 分片响应 `Frame{ShardID string; Claimed int; Records []Record; MaxScore int64}`：
  `Claimed` 是分片自称的条数；`MaxScore` 是该分片给出的**本分片 Score 上界**
  （本分片任何记录的 Score 不超过它），用于 TopK 可信前缀推导。
- 契约判定：
  - `len(Records) != Claimed` → 损坏（`ErrCorrupt`），整分片判失败，坏数据绝不并入。
  - 同一 ShardID 第二次成功投递 → 重复投递（`ErrDuplicate`），只接受第一次
    （模拟重试叠加），不重复计数。
  - 分片超时/被 context 取消 → `ErrTimeout`；无分片或全部失败 → `ErrNoResults`。
  四种故障用独立错误哨兵 + 每分片状态枚举
  `StatusOK/Corrupt/Duplicate/Timeout/Failed` 区分，可用 `errors.Is` 判定。

## 2. 扇出与资源约束

- `fanout.Run`：容量为 limit 的 buffered chan 作为信号量，每票代表一个在飞请求；
  拿票后 `inflight++` 并在互斥锁内刷新非导出峰值 `peakInflight`，放票前 `inflight--`。
  故峰值 ≤ limit（测试 200 分片/上限 8，断言峰值恰不超过 8）。
- 整体截止时间：派生 `context.WithDeadline`。截止后 ctx 结束，拿票循环不再发起
  任何新请求（未发起者直接记 Timeout）；已在飞请求随 ctx.Done 被取消。
  Wait 等待全部 worker 退出，故截止后一个很短窗口内必然返回。
- 每张分片只接受首次成功结果；输出按 ShardID 排序，保证与到达顺序无关的确定性。

## 3. 五种聚合在部分失败下能说什么

设成功分片的观测值为 V，失败分片集合 F 非空。

- Count：真实值 = V + Σ缺失分片计数 ≥ V。观测值是**下界**：标注 `at-least`，区间 [V, +∞)。
- Sum：真实值 = V + Σ缺失分片之和。度量非负时同理 ≥ V：`at-least`，[V, +∞)。
- Min：真实全局 Min = min(V, 缺失分片最小值) ≤ V。只能保证“真实 Min **不大于**当前值”：
  观测值是**上界**，标注 `at-most`，区间 (−∞, V]。
- Max：真实全局 Max = max(V, 缺失分片最大值) ≥ V。只能保证“真实 Max **不小于**当前值”：
  观测值是**下界**，标注 `at-least`，区间 [V, +∞)。
  方向检查：Min 为 at-most、Max 为 at-least，写反即错。
- TopK：观测列表 O 按 (Score 降序, ID 升序) 排序。
  - 列表层面：即使只缺一个分片，当前第 K 名也可能被缺失条目挤掉，故完整 K 列表不可信。
  - 缺失侧上界：`B = Σ_{f∈F} MaxScore_f`（各缺失分片上界之和，是缺失条目可能达到的
    最高得分的保守上界：无论缺失记录如何组合成真实条目，其得分都不可能超过 B）。
  - 条目 x 满足 `Score(x) > B` 时，任何缺失候选得分都 ≤ B < Score(x)，不可能有缺失条目
    排在 x 之前，故 x **确定入选真实 TopK**，且其相对名次确定（并列按 ID 升序打破，
    缺失条目最多与之相等而无法超越，ID 更小且相等也只能在其不超过 B 的前提之外才可能——
    严格大于 B 已排除该情况）。
  - 可信前缀长度 `p = min(K, O 中满足 Score > B 的前缀条目数)`；由于 O 已按分数排序，
    这些条目连续位于前部；从首个 `Score ≤ B` 的条目起入选不再确定，前缀终止。
  - 标注：F 为空 → `exact`（p=K）；p=K（在部分失败下）→ 列表仍精确，标 `exact`；
    0 < p < K → `partial`（仅前 p 名可信）；p=0 → `unbounded`（无一可信）。

边界：K 大于总条目数时只返回实际存在的条目；“所有成功分片都返回空”与“全部失败”可区分
（前者存在 StatusOK 分片、结果为空且 exact，后者无任何成功分片 → `ErrNoResults`）；
分片 ID 为空串是合法 ID；零分片同样返回 `ErrNoResults`；字段缺失的记录跳过缺失字段对应聚合。

## 4. 区间估计汇总

全部成功（F 空）：五种聚合一律 `exact`，区间退化为点，不得一律降级。
部分成功：Count/Sum=[V,+∞) at-least；Min=(−∞,V] at-most；Max=[V,+∞) at-least；
TopK 给可信前缀 p 与缺失上界 B。全部失败：返回可判定错误，不返回零值标 exact。

## 5. 确定性

合并只依赖 (ShardID, Record) 多重集：Count/Sum/Min/Max 交换结合；
TopK 排序键 (Score 降, ID 升) 构成全序；缺失清单按 ShardID 升序；重复投递去重。
故打乱 20 种到达顺序，合并结果与缺失清单逐字节相同；`-race` 干净。

## 6. 测试策略

全部表驱动，同类断言合并为一张表、一个测试函数跑完；逐字节遍历用循环覆盖，不展开用例。
覆盖：五聚合 × 成功/缺一/缺一半；并发峰值 ≤ 8；截止后新发起数为 0 且 Wait 短窗口返回；
超时/损坏/重复/全失败；20 种顺序确定性；零分片、单片、空结果、K 过大、部分字段、
空串 ID；`go test -race`。
