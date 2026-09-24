# 分片扇出部分失败合并器设计

## 1. 模型与接口

- `shard.Record{ID string; Score int64; Valid bool}`：`ID` 全局唯一、可为空串；
  `Valid=false` 表示只返回了部分字段（缺 Score），仍计入 Count，但不进 Sum/Min/Max/TopK 的值运算。
- `shard.Response{Count int64; Records []Record; Bound int64; OK bool}`：
  `Count` 是分片自声称的总条数；`Bound` 是分片内 Score 的上界（含缺失时可能出现的条目）。
- 故障判定：`Count != len(Records)` 或 `OK=false` 视为损坏分片，整分片丢弃，不并入任何结果。
- 重复返回：假分片可配置重试叠加；扇出侧以 `(shardID, seq)` 去重，同一序号结果只生效一次（`replayOK`）。
- 分片状态枚举：`StatusOK / StatusTimeout / StatusCorrupt / StatusDuplicate / StatusFailed`。

## 2. 扇出与资源约束

- 信号量限并发，加锁维护「历史峰值在飞数」`peakInFlight`（非导出）。
- 整体 `context.WithTimeout` 控制截止时间；截止后 worker 循环不再发起请求，已在飞请求 ctx 被取消。
- 截止/取消统一归为 `StatusTimeout`。

## 3. 五种聚合的部分结果推导

设成功分片集合 S，缺失集合 M，真实值记 `*`。

### Count / Sum（单边界：下界）

缺失分片只可能贡献非负数量/分数（Score ≥ 0），故
`c* = c_S + c_M ≥ c_S`，`s* ≥ s_S`。标注「≥ 当前值」，置信度 Partial。

### Min / Max（方向相反的单边界，最易错点）

- Min：成功部分最小值 `m_S` 只是"目前见到的"。缺失分片可能含更小值，
  故 `m* ≤ m_S`（当前值是真实值的上界），标注「真实 Min ≤ 当前值」。
- Max：缺失分片可能含更大值，故 `x* ≥ x_S`（当前值是真实值的下界），标注「真实 Max ≥ 当前值」。
- 成功分片均无有效值时该聚合不可判定，标注 Unknown。

### TopK（可信前缀）

设 `B = Σ_{m∈M} Bound_m`：缺失分片能"挤入"的分值总预算上界。当前列表第 j 名得分 `t_j`。

**入选确定条件**：若 `t_j > B`，缺失侧要出现一个严格高于它的条目所需分值 > B，不可能；
故它必定入选全局 TopK。缺失条目仍可能插入到这些确定条目之间，所以确定的只是入选、不是名次。
可信前缀长度 `p = max{ j : t_1 ≥ … ≥ t_j > B }`（连续前缀，首个不满足即止）：

- 无缺失：`p = min(K, n)`，整体 Exact。
- 缺失且前 K 名都 > B（Bound 很小）：`p = K`，排名仍标 Partial。
- 缺失且 `B ≥ t_1`（Bound 很大）：`p = 0`，整个列表不可信。

并列按 `(Score 降序, ID 升序)` 排序，与到达顺序无关。

## 4. 边界与错误

- 零分片：`ErrNoShards`；全部失败：`ErrAllFailed`；全部成功但为空：合法零结果 + Exact，与全失败可区分。
- K > 总条目数：返回全部条目，前缀长度 = 实际长度。空 ID 合法，参与并列排序。
- 全部成功：五种聚合一律 Exact，不降级。

## 5. 确定性

合并只依赖按 ID 排序后的条目多重集；缺失清单按分片 ID 排序；报告序列化字节一致。
重复返回经序号去重，Count/Sum 与单次返回逐位相同。

## 6. 包划分

`shard`（接口+假实现）`fanout`（并发）`combine`（五聚合）
`confidence`（标注+区间）`report`（报告）`cmd/demo`。
测试每包合并为一个表驱动文件；全项目 .go 文件 9 个，每个 ≤200 行。
