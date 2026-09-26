# 加权中位数 NOTES

## 第三节推导

依次 Insert(30,3)、Insert(10,3)、Insert(40,3)、Insert(20,3)，W = 12，W/2 = 6。
按 value 升序排列后逐步求前缀权重：

| 步骤 | value | weight | 前缀权重 P_k |
|---|---|---|---|
| 1 | 10 | 3 | 3 |
| 2 | 20 | 3 | 6 |
| 3 | 30 | 3 | 9 |
| 4 | 40 | 3 | 12 |

- (甲) 正确加权中位数 = **20**（P_2 = 6 是首个 ≥ 6 的前缀）。若错成「忽略权重取普通中位数」，排序后中间两个 value 为 20、30，平均 = **25**，错成 25。
- (乙) 若错写成 `P_k > W/2`（严格大于）：P_2 = 6 不满足 6 > 6，首个满足的是 P_3 = 9，错成 **30**。
- (丙) 若错写成 `P_k ≥ W`（忘记除以 2）：首个 ≥ 12 的前缀是 P_4 = 12，错成 **40**。

## 四条不变量及其保证位置与钉住测试

1. **两侧权重约束**：`median/median.go` 的 `Find` 沿树用整数等价式 `2*P_k ≥ 2*(W/2)` 即 `2*P_k ≥ W` 定位，命中节点左侧权重和 < W/2、右侧权重和 ≤ W/2；由 `TestSideWeightInvariant` 钉住。
2. **最小性**：`Find` 只在「左子树权重和 + 已累加权重仍不足 W/2」时才右移，命中即停，故返回的是满足条件的最小 value；由 `TestSideWeightInvariant` 中的最小性断言（`2*below >= W` 即报错）钉住。
3. **与朴素参照一致**：`api/api_test.go` 的 `naiveMedian` 排序扫描作参照；由 `TestMedianMatchesNaive`（多档规模 × 随机顺序）钉住。
4. **失败不留痕**：`wmid/wmid.go` 的 `Insert` 先验权重、查重，全部通过才改树；空集合 `Find` 直接返回 `ErrEmpty`；由 `TestRejectedOpsNoStateChange` 钉住。
