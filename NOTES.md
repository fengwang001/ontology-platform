# NOTES — 计数布隆过滤器

## 一、推导（m=8, k=3, h_j(x)=(j·x) mod 8）

| 步骤 | (h1,h2,h3) | c[0..7] |
|---|---|---|
| Add(3) | (3,6,1) | [0 1 0 1 0 0 1 0] |
| Add(5) | (5,2,7) | [0 1 1 1 0 1 1 1] |
| Add(7) | (7,6,5) | [0 1 1 1 0 2 2 2] |
| Remove(5) | (5,2,7) | [0 1 0 1 0 1 2 1] |

- (甲) Remove(5) 后 Query(5)=min(c5,c2,c7)=min(1,0,1)=**0 → 不存在**。若错写成「取 max、任一 ≥1」：max=1 → **误判为存在**（假阳性）。
- (乙) 再 Remove(5) 一次：c[h2(5)]=c[2]=0，正确实现报「删除不存在」哨兵错误且不动任何计数器。无下溢保护的朴素实现会把 **c[2] 从 0 减到 −1**。
- (丙) Remove(5) 之前 Query(3)：h=(3,6,1)，计数为 (c3,c6,c1)=(1,2,1)，**min=1**（min 来自 c[1] 与 c[3]）。若错用单哈希 c[h2(3)]=c[6]=**2**，是被 **Add(7)** 高估的（h2(7)=14 mod 8=6 也命中 c[6]）。

## 二、四条不变量的保证位置与钉住它的测试

1. 无假阴性：`cbf.Add` 对 k 个计数器全部 +1、`cbf.Query` 取 min（cbf/cbf.go）→ `TestNoFalseNegative`
2. 精确移除：`cbf.Remove` 对同一组 k 个下标对称 −1（cbf/cbf.go）→ `TestExactRemoval`
3. 与朴素重放一致：`cbf.Add/Remove` 逐条、逐项应用 ±1，无旁路状态（cbf/cbf.go）→ `TestNaiveReplayConsistency`
4. 失败不留痕：`api` 先校验 m/k/key 再动状态；`cbf.Remove` 先确认 k 个计数器全 ≥1 才统一 −1（api/api.go、cbf/cbf.go）→ `TestFailedOpNoSideEffect`

另：查询复杂度（Query 只访问 k 个计数器，非导出字段 `lastVisits` 记录）→ `TestQueryVisitsExactlyK`（cbf 包内白盒测试）；并发一致 → `TestConcurrentQueryConsistent`；三类哨兵错误互不相同 → `TestErrorsDistinct`。
