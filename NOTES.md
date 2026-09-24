# NOTES — 可撤回 TopK 维护器

## 一、七步推导（K=3，排序键：score 降序、id 升序）

| # | 操作 | 完整有序序列 | TopK() |
|---|------|--------------|--------|
| 1 | Add(m,10) | m10 | m10 |
| 2 | Add(a,10) | a10, m10 | a10, m10 |
| 3 | Add(z,20) | z20, a10, m10 | z20, a10, m10 |
| 4 | Add(y,20) | y20, z20, a10, m10 | y20, z20, a10 |
| 5 | Add(b,5) | y20, z20, a10, m10, b5 | y20, z20, a10 |
| 6 | Remove(z) | y20, a10, m10, b5 | y20, a10, m10 |
| 7 | Add(k,10) | y20, a10, k10, m10, b5 | y20, a10, k10 |

- (甲) 第4步：y、z 同 20 分，id 升序 y<z，故 **y 在 z 前**，TopK={y,z,a}。若错用「先到先排」，序列变 z20,y20,a10,m10，TopK 错成 **{z,y,a}**（z 顶替 y 居首）。
- (乙) 第6步：补位者是门槛外最高分 **m（m10）**。若实现只保留 TopK 集合、把被挤出的 m 直接丢弃，Remove(z) 后 TopK 错成 **{y,a}**——少了 m，且只剩 2 个元素。
- (丙) 第7步：a、k、m 同 10 分争 y 之后两个名额，id 升序 a<k<m，正确规则**保留 a、k，排除 m**。若按插入先后（m 早于 a 早于 k）则**保留 m、a，排除 k**。同一规则集两种结果 → 并列处必须有确定性次级键（id 升序），否则名次不唯一、前缀一致性（不变量3）不成立。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`topk.Main.rebalance` 维持「top 堆活节点 = 全序前 K」；测试 `TestNaiveEquivalence`（随机操作序列与朴素排序对拍）。
2. 撤回正确：`topk.Main.rebalance` 从 rest 堆顶补位；测试 `TestPromotionAfterRemove`。
3. 确定性与前缀一致：`ord.Less` 的全序比较器（score 降序、id 升序）；测试 `TestPrefixConsistency`。
4. 失败不留痕：`api.New/Add/Remove` 先做完全部校验再触碰状态；测试 `TestRejectedOpsNoSideEffect`。
