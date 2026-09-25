# 快照合并：推导与不变量落点

## 八步推导（段1=v1–v5，段2=v6–v8；合并入参顺序：段2 在前、段1 在后）

| # | 记录 | 该键当前最新记录 | 该键此刻活值 | 全表活值快照 |
|---|---|---|---|---|
| 1 | v1 a put x  | a←put x (v1)  | x      | a=x |
| 2 | v2 b put y  | b←put y (v2)  | y      | a=x, b=y |
| 3 | v3 a put x2 | a←put x2 (v3) | x2     | a=x2, b=y |
| 4 | v4 c put z  | c←put z (v4)  | z      | a=x2, b=y, c=z |
| 5 | v5 b del    | b←del (v5)    | 已删除 | a=x2, c=z |
| 6 | v6 a del    | a←del (v6)    | 已删除 | c=z |
| 7 | v7 c put z2 | c←put z2 (v7) | z2     | c=z2 |
| 8 | v8 b put y2 | b←put y2 (v8) | y2     | b=y2, c=z2 |

- **(甲)** `a` 最终**不存在**（v6 墓碑为最大版本，被回收）。错实现「忽略墓碑、只取最大版本 put」会把 `a` 错成 **x2 (v3)**——已删除的键重新出现，即**数据复活**。
- **(乙)** `Watermark` = **8**（所有输入记录的最大版本）。错实现「取按 Key 升序输出的最后一条记录的版本」：输出为 b(v8)、c(v7)，末条是 c → 水位错成 **7**；下游从 8 起回放，会把 **v8（b put y2）重复回放**一次。
- **(丙)** 错实现「段列表中后出现的段覆盖先出现的段」，段2 在前、段1 在后 → 段1（旧版本）胜出：`b` 错成**已删除**（v5 墓碑盖掉 v8 的 y2），`c` 错成 **z**（v4 盖掉 v7 的 z2）。

## 四条不变量：代码落点与钉住它的测试

1. **与批量重算一致**：`api.Ingest` 因版本全局严格递增，新记录即该键最大版本，直接刷新 `latest[key]`（api.go）；`TestViewMatchesBatchRecompute` 逐键对比独立批量重算。
2. **合并结果自洽**：`comp.Merge` 用 map 按键去重取最大版本、`sort` 按 Key 升序、跳过全部 `del`（comp.go）；`TestCompactSelfConsistent`。
3. **水位单调**：`api.maxVer` 只在 `Ingest`/`Compact` 中取 max 单调更新，`Watermark()` 只读它（api.go）；`TestWatermarkMonotonic`。
4. **失败不留痕**：`api.Ingest`/`Compact` 先完成全部校验、通过后才写任何状态（api.go 校验前置）；`TestRejectedOpsLeaveNoTrace`。
